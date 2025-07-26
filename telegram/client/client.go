package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/kittenbark/tg"
	"io"
	"net/http"
	"os"
	"time"
)

var (
	Endpoint          = "http://localhost:6969"
	EndpointImageNsfw = "/v1/image_nsfw"
	EndpointHealth    = "/health"
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
	if endpoint, ok := os.LookupEnv("MODESTY_ENDPOINT"); ok {
		Endpoint = endpoint
	}
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
