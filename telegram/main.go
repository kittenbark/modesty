package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kittenbark/smoldb"
	"github.com/kittenbark/tg"
	"modesty/telegram/client"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	Chats *smoldb.Smol[int64, ChatInfo]
)

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

type ChatInfo struct {
	Id        int64   `json:"id"`
	Threshold float64 `json:"threshold"`
	Comments  bool    `json:"comments,omitempty"`
	Debug     bool    `json:"debug,omitempty"`
}

func init() {
	var err error
	Chats, err = smoldb.New[int64, ChatInfo](env("MODESTY_TG_CHATS", "./modesty_chats.yaml"))
	if err != nil {
		panic(err)
	}
}

type Action func(ctx context.Context, msg *tg.Message, filename string) error

func WeightAndTagAdmin(ctx context.Context, msg *tg.Message, filename string) error {
	info, ok, err := Chats.TryGet(msg.Chat.Id)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if info.Comments && msg.ReplyToMessage == nil {
		return nil
	}

	start := time.Now()
	nsfw, err := client.ImageNsfw(ctx, filename)
	if err != nil {
		return err
	}
	if !nsfw.IsNsfw && !evaluationRequest(msg) {
		return nil
	}

	if !info.Debug && nsfw.Certainty >= info.Threshold {
		if _, err = tg.DeleteMessage(ctx, msg.Chat.Id, msg.MessageId); err != nil {
			return err
		}
	}

	_, err = tg.SendMessage(
		ctx,
		msg.Chat.Id,
		fmt.Sprintf(
			"```yaml\nnsfw: %t\ncert: %s\ntook: %s\n```",
			nsfw.IsNsfw,
			tg.Md(fmt.Sprintf("%.3f", nsfw.Certainty)),
			tg.Md(time.Since(start).String()),
		),
		&tg.OptSendMessage{
			DisableNotification: true,
			ReplyParameters: &tg.ReplyParameters{
				MessageId:                msg.MessageId,
				ChatId:                   msg.Chat.Id,
				AllowSendingWithoutReply: true,
			},
			ParseMode: tg.ParseModeMarkdownV2,
		},
	)
	return err
}

type File func(ctx context.Context, msg *tg.Message) (string, error)

func Handler(file File, action Action) tg.HandlerFunc {
	name := runtime.FuncForPC(reflect.ValueOf(file).Pointer()).Name()
	return func(ctx context.Context, upd *tg.Update) (err error) {
		defer func() {
			if err != nil {
				err = fmt.Errorf("%v: %w", name, err)
			}
		}()

		msg := upd.Message
		filename, err := file(ctx, msg)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, os.Remove(filename)) }()

		return action(ctx, msg, filename)
	}
}

func OnPhoto(ctx context.Context, msg *tg.Message) (string, error) {
	return msg.Photo.DownloadTemp(ctx)
}
func OnVideo(ctx context.Context, msg *tg.Message) (string, error) {
	return msg.Video.Thumbnail.DownloadTemp(ctx)
}
func OnVideoNote(ctx context.Context, msg *tg.Message) (string, error) {
	return msg.VideoNote.Thumbnail.DownloadTemp(ctx)
}
func OnAnimation(ctx context.Context, msg *tg.Message) (string, error) {
	return msg.Animation.Thumbnail.DownloadTemp(ctx)
}

func FilterActivatedChats() tg.FilterFunc {
	return tg.All(tg.OnMessage, func(ctx context.Context, upd *tg.Update) bool {
		if _, err := Chats.Get(upd.Message.Chat.Id); err != nil {
			fmt.Printf("%v %v\n", upd.Message.Chat.Id, Chats.Keys())
			println(err.Error())
			return false
		}
		return true
	})
}

func HandlerActive() tg.HandlerFunc {
	return func(ctx context.Context, upd *tg.Update) error {
		msg := upd.Message
		args := strings.Fields(msg.TextOrCaption())
		if len(args) < 2 {
			_, err := tg.SendMessage(ctx, msg.Chat.Id, "usage: /activate <threshold> <comments> <debug>", &tg.OptSendMessage{ReplyParameters: tg.AsReplyTo(msg)})
			return err
		}

		info := ChatInfo{Id: msg.Chat.Id}
		threshold, err := strconv.ParseFloat(args[1], 64)
		if err != nil || threshold < 0 || threshold > 1 {
			_, err := tg.SendMessage(ctx, msg.Chat.Id, "usage: /activate 0.9", &tg.OptSendMessage{ReplyParameters: tg.AsReplyTo(msg)})
			return err
		}
		info.Threshold = threshold

		if len(args) > 2 {
			if comments, err := strconv.ParseBool(args[2]); err == nil {
				info.Comments = comments
			}
		}
		if len(args) > 3 {
			if debug, err := strconv.ParseBool(args[3]); err == nil {
				info.Debug = debug
			}
		}

		if err = Chats.Set(msg.Chat.Id, info); err != nil {
			return err
		}
		return nil
	}
}

func EvaluationRequestAsOriginal(ctx context.Context, upd *tg.Update) bool {
	if upd.Message == nil {
		return true
	}

	msg := upd.Message
	switch {
	case msg.ReplyToMessage == nil:
	case msg.Photo != nil || msg.Animation != nil || msg.Voice != nil || msg.VideoNote != nil:
	case !evaluationRequest(msg):
	default:
		upd.Message = msg.ReplyToMessage
		upd.Message.Caption += "\nnsfw?"
	}
	return true
}

func evaluationRequest(msg *tg.Message) bool {
	txt := strings.ToLower(msg.TextOrCaption())
	return strings.Contains(txt, "nsfw") || strings.Contains(txt, "нсфв")
}

func main() {
	if err := client.EndpointHealthy(); err != nil {
		panic(err)
	}

	tg.NewFromEnv().
		OnError(tg.OnErrorLog).
		Scheduler().
		Command("/start", tg.CommonTextReply("modesty is virtue")).
		Command("/activate", HandlerActive()).
		Filter(tg.OnMessage, FilterActivatedChats(), EvaluationRequestAsOriginal).
		Branch(tg.OnPhoto, Handler(OnPhoto, WeightAndTagAdmin)).
		Branch(tg.OnVideo, Handler(OnVideo, WeightAndTagAdmin)).
		Branch(tg.OnVideoNote, Handler(OnVideoNote, WeightAndTagAdmin)).
		Branch(tg.OnAnimation, Handler(OnAnimation, WeightAndTagAdmin)).
		Default(func(ctx context.Context, upd *tg.Update) error {
			data, _ := json.MarshalIndent(upd.Message, "", "  ")
			println(string(data))
			return nil
		}).
		Start()
}
