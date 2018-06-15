/*
Package gcloudvoice is a utility package that was created to easily transcribe
dual channel twilio phone recordings. At the time of writing
Google's Speech API does not accept dual channel inputs and it also does not
allow for specifying non-google-storage (gs://) URIs. This package is meant to
fill those gaps.
*/
package gcloudvoice

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/sync/errgroup"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/storage"
	"github.com/golang/protobuf/ptypes"
	"github.com/pkg/errors"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1"
)

var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

// Message is a transcribed section of a conversation.
type Message struct {
	// Channel is 0 or 1 indicating left or right channel.
	// This would be used to identify the caller/called speaker in a phone
	// conversation.
	Channel bool
	Offset  time.Duration
	Text    string
}

// ByTime is a type that conforms to the `sort` package for sorting
// messages in chronological order.
type ByTime []Message

func (s ByTime) Len() int           { return len(s) }
func (s ByTime) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ByTime) Less(i, j int) bool { return s[i].Offset < s[j].Offset }

// Client wraps google `storage` and `speech` clients.
type Client struct {
	KeepIntermediateFiles bool
	StorageBucket         string
	Storage               *storage.Client
	Speech                *speech.Client
}

// TranscribeURL grabs a stereo `wav` file from an http url. It splits the
// channels using a system call to `ffmpeg` and transcribes the messages and
// combines them into a single slice of messages. It does not sort them by
// time, so a subsequent call to `sort.Sort(gcloudvoice.ByTime(msgs))` is
// needed for most use cases.
func (c *Client) TranscribeURL(ctx context.Context, url, name string) (msgs []Message, rerr error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, errors.Wrap(err, "unable to GET url")
	}
	defer resp.Body.Close()

	if name == "" {
		name = path.Base(url)
	}
	name = strings.TrimSuffix(name, ".wav")
	leftName := name + ".left.wav"
	rightName := name + ".right.wav"

	bkt := c.Storage.Bucket(c.StorageBucket)

	if !c.KeepIntermediateFiles {
		// Cleanup gcloud storage objects.
		defer func() {
			if err := bkt.Object(leftName).Delete(ctx); err != nil && err != storage.ErrObjectNotExist {
				rerr = multierror.Append(rerr, errors.Wrap(err, "deleting left channel file from gcloud"))
			}
			if err := bkt.Object(rightName).Delete(ctx); err != nil && err != storage.ErrObjectNotExist {
				rerr = multierror.Append(rerr, errors.Wrap(err, "deleting right channel file from gcloud"))
			}
		}()
	}

	leftObj := bkt.Object(leftName).NewWriter(ctx)
	rightObj := bkt.Object(rightName).NewWriter(ctx)
	if err := splitWavChannels(resp.Body, leftObj, rightObj); err != nil {
		return nil, errors.Wrap(err, "splitting wav")
	}

	// Close must be called before another process can read.
	if err := leftObj.Close(); err != nil {
		rightObj.Close()
		return nil, errors.Wrap(err, "closing left gcloud storage writer")
	}
	if err := rightObj.Close(); err != nil {
		return nil, errors.Wrap(err, "closing right gcloud storage writer")
	}

	gsPath := func(name string) string {
		return fmt.Sprintf("gs://%s/%s", c.StorageBucket, name)
	}

	leftMsgs, rightMsgs := make(chan []Message), make(chan []Message)
	var transcribeGrp errgroup.Group
	transcribeGrp.Go(func() error {
		msgs, err := transcribeChannel(ctx, c.Speech, gsPath(leftName), true)
		if err != nil {
			leftMsgs <- nil
			return errors.Wrap(err, "left channel")
		}
		leftMsgs <- msgs
		return nil
	})
	transcribeGrp.Go(func() error {
		msgs, err := transcribeChannel(ctx, c.Speech, gsPath(rightName), false)
		if err != nil {
			rightMsgs <- nil
			return errors.Wrap(err, "right channel")
		}
		rightMsgs <- msgs
		return nil
	})

	return append(<-leftMsgs, <-rightMsgs...), errors.Wrap(transcribeGrp.Wait(), "transcribing")
}

// splitWavChannels splits a stereo `wav` format input into its two channels.
// It assumes `ffmpeg` is installed an in the $PATH.
func splitWavChannels(in io.Reader, left, right io.Writer) error {
	// If this fails the error msg will be lost b/c we are abusing
	// stderr. However, the code to incorporate named pipes in not
	// worth the increased complexity IMO.
	cmd := exec.Command("ffmpeg",
		"-y",
		"-loglevel", "panic",
		// Input from stdin.
		"-i", "pipe:0",
		// Output to stdout.
		"-f", "wav", "-map_channel", "0.0.0", "pipe:1",
		// Output to stderr.
		"-f", "wav", "-map_channel", "0.0.1", "pipe:2",
	)

	// Map pipes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.Wrap(err, "opening stdin pipe")
	}
	cmd.Stderr = left
	cmd.Stdout = right

	if err := cmd.Start(); err != nil {
		return errors.Wrapf(err, "starting command")
	}

	if _, err := io.Copy(stdin, in); err != nil {
		return errors.Wrap(err, "copying stdin")
	}
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		return errors.Wrapf(err, "waiting for command to finish")
	}

	return nil
}

// transcribeChannel reaches out to google's speech to text api and transcribes
// a single wav channel.
func transcribeChannel(ctx context.Context, c *speech.Client, uri string, chn bool) ([]Message, error) {
	op, err := c.LongRunningRecognize(ctx, &speechpb.LongRunningRecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:              speechpb.RecognitionConfig_LINEAR16,
			SampleRateHertz:       8000,
			LanguageCode:          "en-US",
			EnableWordTimeOffsets: true,
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Uri{Uri: uri},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "starting longrunning recognize")
	}

	resp, err := op.Wait(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "waiting on longrunning recognize")
	}

	// Parse the results.
	var msgs []Message
	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 || len(result.Alternatives[0].Words) == 0 {
			continue
		}
		alt0 := result.Alternatives[0]
		word0 := alt0.Words[0]

		dur, err := ptypes.Duration(word0.StartTime)
		if err != nil {
			return nil, errors.Wrap(err, "converting word duration")
		}

		msgs = append(msgs, Message{
			Channel: chn,
			Offset:  dur,
			Text:    alt0.Transcript,
		})
	}

	return msgs, nil
}
