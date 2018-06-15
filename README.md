# Google Speech Utilities

Utilities to make working with Google Speech APIs easier.

## Usage

We use this library in junction with Twilio recordings (b/c the twilio transcription add-ons, at the time of writing, are junk):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Dial timeout="10" record="record-from-answer-dual" recordingStatusCallback="https://my.service.com">415-123-4567</Dial>
</Response>
```

In the callback http handler:

```go
// Transcribe the call.
msgs, err := client.TranscribeURL(ctx, twilioRecordingURL, "")
if err != nil {
	return err
}

// Sort the messages.
sort.Sort(gcloudvoice.ByTime(msgs))
```

## Testing

```sh
# Setup test configuration.
export GCLOUDVOICE_TEST_STORAGE_BUCKET=my-gcloud-bucket
export GCLOUDVOICE_TEST_RECORDING=my-recording.wav
export GOOGLE_APPLICATION_CREDENTIALS=my-google-creds.json

# Run tests against your gcloud account.
go test -v .
```

