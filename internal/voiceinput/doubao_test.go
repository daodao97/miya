package voiceinput

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/microphone"
)

func TestTranscriptRevisesSentencesAndHandlesCumulativeBackend(t *testing.T) {
	var tr transcript
	for _, raw := range []string{
		`{"results":[{"index":0,"text":"你好","extra":{"nonstream_result":true}}]}`,
		`{"results":[{"index":1,"text":"世界","is_interim":false,"is_vad_finished":true}]}`,
		`{"results":[{"index":0,"text":"您好，","extra":{"nonstream_result":true}}]}`,
	} {
		if err := tr.update(raw); err != nil {
			t.Fatal(err)
		}
	}
	if got := tr.text(); got != "您好，世界" {
		t.Fatalf("sentence revision = %q", got)
	}
	if err := tr.update(`{"results":[{"text":"完整结果"},{"text":"辅助候选"}]}`); err != nil {
		t.Fatal(err)
	}
	if got := tr.text(); got != "完整结果" {
		t.Fatalf("cumulative backend = %q", got)
	}
}

func TestParseResponseRejectsTruncationAndKeepsUnknownFields(t *testing.T) {
	b := pbBytes(nil, 4, []byte("SessionFinished"))
	b = pbBytes(b, 99, []byte("future"))
	b = pbBytes(b, 7, []byte(`{"results":[]}`))
	r, err := parseResponse(b)
	if err != nil || r.event != "SessionFinished" || r.result == "" {
		t.Fatalf("parse = %+v %v", r, err)
	}
	if _, err := parseResponse(b[:len(b)-1]); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated response = %v", err)
	}
}

func TestCaptureOverflowClosesBoundedBuffer(t *testing.T) {
	s := &recording{audio: make(chan []byte, 1)}
	s.capture(microphone.Chunk{PCM: []byte{1, 2}})
	s.capture(microphone.Chunk{PCM: []byte{3, 4}})
	if !s.closed || s.captureError == nil {
		t.Fatal("overflow did not end capture")
	}
	s.capture(microphone.Chunk{PCM: []byte{5, 6}})
	if b := <-s.audio; len(b) != 2 || b[0] != 1 {
		t.Fatal("capture corrupted buffered audio")
	}
	if _, ok := <-s.audio; ok {
		t.Fatal("audio not closed")
	}
}

func TestDoubaoLive(t *testing.T) {
	file := os.Getenv("MIYA_VOICE_TEST_WAV")
	if file == "" {
		t.Skip("set MIYA_VOICE_TEST_WAV to explicitly send a test WAV to Doubao")
	}
	pcm := readVoiceTestPCM(t, file)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	audio := make(chan []byte, 1)
	go func() {
		defer close(audio)
		for len(pcm) > 0 {
			n := min(len(pcm), 3200)
			select {
			case audio <- pcm[:n]:
				pcm = pcm[n:]
			case <-ctx.Done():
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	text, err := recognize(ctx, t.TempDir(), audio, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("empty transcript")
	}
	t.Logf("transcript: %s", text)
}

func readVoiceTestPCM(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatal("expected WAV")
	}
	var pcm []byte
	for i := 12; i+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[i+4:]))
		start := i + 8
		if size > len(b)-start {
			t.Fatal("truncated WAV")
		}
		if string(b[i:i+4]) == "fmt " {
			f := b[start : start+size]
			if size < 16 || binary.LittleEndian.Uint16(f) != 1 || binary.LittleEndian.Uint16(f[2:]) != 1 || binary.LittleEndian.Uint32(f[4:]) != 16000 || binary.LittleEndian.Uint16(f[14:]) != 16 {
				t.Fatal("need 16 kHz mono PCM16 WAV")
			}
		}
		if string(b[i:i+4]) == "data" {
			pcm = b[start : start+size]
			break
		}
		i = start + size + size%2
	}
	if len(pcm) == 0 {
		t.Fatal("no PCM")
	}
	return pcm
}
