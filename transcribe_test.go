package gcloudvoice_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/storage"
	"github.com/upgear/gcloudvoice"
)

func TestTranscribeURL(t *testing.T) {
	cfg := struct {
		RecordingPath string
		StorageBucket string
	}{
		RecordingPath: os.Getenv("GCLOUDVOICE_TEST_RECORDING"),
		StorageBucket: os.Getenv("GCLOUDVOICE_TEST_STORAGE_BUCKET"),
	}
	if cfg.RecordingPath == "" {
		t.Fatal("missing env var `GCLOUDVOICE_TEST_RECORDING`")
	}
	if cfg.StorageBucket == "" {
		t.Fatal("missing env var `GCLOUDVOICE_TEST_STORAGE_BUCKET`")
	}

	wav, err := ioutil.ReadFile(cfg.RecordingPath)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(wav); err != nil {
			t.Fatal(err)
		}
	}))

	ctx := context.Background()
	storage, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	speech, err := speech.NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}

	client := gcloudvoice.Client{
		StorageBucket: cfg.StorageBucket,
		Storage:       storage,
		Speech:        speech,
		// KeepIntermediateFiles: true,
	}
	msgs, err := client.TranscribeURL(ctx, ts.URL, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Sort the messages.
	sort.Sort(gcloudvoice.ByTime(msgs))

	fmt.Println(msgs)
}
