package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kittenbark/nanodb/jsondb"
	"github.com/kittenbark/tg"
	"io"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	Endpoint          = "http://localhost:6969"
	EndpointImageNsfw = "/v1/image_nsfw"
	EndpointHealth    = "/health"
	DB                *jsondb.Cached[ChatInfo]
)

type ChatInfo struct {
	Id        int64   `json:"id"`
	Threshold float64 `json:"threshold"`
	Comments  bool    `json:"comments,omitempty"`
	Debug     bool    `json:"debug,omitempty"`
}

func init() {
	if endpoint, ok := os.LookupEnv("MODESTY_ENDPOINT"); ok {
		Endpoint = endpoint
	}

	cache, ok := os.LookupEnv("MODESTY_TG")
	if !ok {
		cache = "./chats.json"
	}
	db, err := jsondb.New[ChatInfo](cache)
	if err != nil {
		panic(err)
	}
	DB = db
}

type Action func(ctx context.Context, msg *tg.Message, filename string) error

type Response struct {
	IsNsfw    bool    `json:"nsfw"`
	Certainty float64 `json:"certainty"`
}

func ImageNsfw(ctx context.Context, filename string) (result *Response, err error) {
	type Request struct {
		ImageData []byte `json:"image_data"`
	}
	req := new(Request)

	if req.ImageData, err = os.ReadFile(filename); err != nil {
		return nil, err
	}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	reqHttp, err := http.NewRequestWithContext(ctx, "POST", Endpoint+EndpointImageNsfw, bytes.NewBuffer(reqData))
	if err != nil {
		return nil, err
	}

	respHttp, err := http.DefaultClient.Do(reqHttp)
	if err != nil {
		return nil, err
	}
	defer func(body io.ReadCloser) { _ = body.Close() }(respHttp.Body)
	if respHttp.StatusCode != 200 {
		return nil, errors.New(respHttp.Status)
	}

	var resp Response
	if err := json.NewDecoder(respHttp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func EndpointHealthy() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", Endpoint+EndpointHealth, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	return nil
}

func WeightAndTagAdmin(ctx context.Context, msg *tg.Message, filename string) error {
	info, ok, err := DB.TryGet(strconv.FormatInt(msg.Chat.Id, 10))
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
	nsfw, err := ImageNsfw(ctx, filename)
	if err != nil {
		return err
	}
	if !nsfw.IsNsfw {
		if txt := strings.ToLower(msg.TextOrCaption()); !strings.Contains(txt, "nsfw") && !strings.Contains(txt, "нсфв") {
			return nil
		}
	}

	if !info.Debug && nsfw.Certainty >= info.Threshold {
		_, err = tg.DeleteMessage(ctx, msg.Chat.Id, msg.MessageId)
		return err
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
			ReplyParameters:     tg.AsReplyTo(msg),
			ParseMode:           tg.ParseModeMarkdownV2,
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
		_, ok, err := DB.TryGet(strconv.FormatInt(upd.Message.Chat.Id, 10))
		if err != nil {
			return false
		}
		return ok
	})
}

func HandlerActive() tg.HandlerFunc {
	return func(ctx context.Context, upd *tg.Update) error {
		msg := upd.Message
		args := strings.Fields(msg.TextOrCaption())
		if len(args) < 2 {
			_, err := tg.SendMessage(ctx, msg.Chat.Id, "usage: /activate 0.9", &tg.OptSendMessage{ReplyParameters: tg.AsReplyTo(msg)})
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
			if comments, err := strconv.ParseBool(args[2]); err != nil {
				info.Comments = comments
			}
		}
		if len(args) > 3 {
			if debug, err := strconv.ParseBool(args[2]); err != nil {
				info.Debug = debug
			}
		}

		if err = DB.Add(strconv.FormatInt(msg.Chat.Id, 10), info); err != nil {
			return err
		}
		return nil
	}
}

func main() {
	if err := EndpointHealthy(); err != nil {
		panic(err)
	}

	tg.NewFromEnv().
		OnError(tg.OnErrorLog).
		Command("/start", tg.CommonTextReply("modesty is virtue")).
		Command("/activate", HandlerActive()).
		Filter(FilterActivatedChats()).
		Branch(tg.OnPhoto, Handler(OnPhoto, WeightAndTagAdmin)).
		Branch(tg.OnVideo, Handler(OnVideo, WeightAndTagAdmin)).
		Branch(tg.OnVideoNote, Handler(OnVideoNote, WeightAndTagAdmin)).
		Branch(tg.OnAnimation, Handler(OnAnimation, WeightAndTagAdmin)).
		Scheduler().
		Start()
}
