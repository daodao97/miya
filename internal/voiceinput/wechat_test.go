package voiceinput

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/klauspost/compress/snappy"
	"github.com/thesyncim/gopus"
)

func TestWechatSharedKeyMatchesServerCurve(t *testing.T) {
	curve := wechatCurve()
	private, x, y, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, ox, oy, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := fmt.Sprintf("04%032X%032X", x, y)
	remote := fmt.Sprintf("04%032X%032X", ox, oy)
	a, err := wechatSharedKey(private, remote)
	if err != nil {
		t.Fatal(err)
	}
	b, err := wechatSharedKey(other, public)
	if err != nil || !bytes.Equal(a, b) || len(a) != 32 {
		t.Fatalf("ECDH key agreement failed: %v", err)
	}
	g := fmt.Sprintf("04%032X%032X", curve.Gx, curve.Gy)
	key, err := wechatSharedKey([]byte{1}, g)
	if err != nil || string(key) != "161FF7528B899B2D0C28607CA52C5B86" {
		t.Fatalf("known shared x = %q, %v", key, err)
	}
	for _, invalid := range []string{"04", strings.Repeat("0", 66), "04" + strings.Repeat("F", 64)} {
		if _, err := wechatSharedKey(private, invalid); err == nil {
			t.Fatal("accepted invalid server public key")
		}
	}
}

func TestWechatEncryptionPaddingAndTranscript(t *testing.T) {
	key := []byte(wechatBootKey)
	for _, data := range [][]byte{nil, []byte("hello"), bytes.Repeat([]byte{1}, 32)} {
		cipher, err := wechatEncrypt(key, data)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := wechatDecrypt(key, cipher)
		if err != nil || !bytes.Equal(plain, data) {
			t.Fatalf("AES round trip: %v", err)
		}
	}
	for _, invalid := range [][]byte{nil, {1}, make([]byte, 16)} {
		if _, err := wechatDecrypt(key, invalid); err == nil {
			t.Fatal("accepted corrupt ciphertext")
		}
	}
	for _, compressed := range []bool{false, true} {
		payload := wechatMessage(nil).data(1, wechatMessage(nil).str(4, "识别中").str(14, "最终文字。"))
		h := make(http.Header)
		if compressed {
			payload = snappy.Encode(nil, payload)
			h.Set("Kb-CompressionType", "2")
		}
		cipher, err := wechatEncrypt(key, payload)
		if err != nil {
			t.Fatal(err)
		}
		text, polished, err := decodeWechatVoice(key, wechatReply{body: cipher, headers: h})
		if err != nil || text != "识别中" || polished != "最终文字。" {
			t.Fatalf("compressed=%v: %q / %q, %v", compressed, text, polished, err)
		}
	}
	// A small encrypted packet must not request an unbounded allocation.
	oversized := binary.AppendUvarint(nil, wechatLimit+1)
	cipher, _ := wechatEncrypt(key, oversized)
	if _, _, err := decodeWechatVoice(key, wechatReply{body: cipher, headers: http.Header{"Compressiontype": {"2"}}}); err == nil {
		t.Fatal("accepted oversized compressed response")
	}
}

func TestWechatReplyParsesRepeatedHeadersAndRejectsTruncation(t *testing.T) {
	response := wechatMessage(nil).num(2, 200).data(3, wechatMessage(nil).str(1, "Kb-CompressionType").str(2, "2")).data(5, []byte("encrypted"))
	frame := wechatMessage(nil).num(3, 7).data(4, response)
	r, err := parseWechatReply(frame)
	if err != nil || r.status != 200 || r.headers.Get("Kb-CompressionType") != "2" || string(r.body) != "encrypted" {
		t.Fatalf("reply: %+v, %v", r, err)
	}
	for _, invalid := range [][]byte{frame[:len(frame)-1], {0}, {0x0a, 0xff}, {0x0f}} {
		if _, err := parseWechat(invalid); err == nil {
			t.Fatalf("accepted invalid protobuf %x", invalid)
		}
	}
	if _, err := parseWechatReply(wechatMessage(nil).data(4, wechatMessage(nil).num(2, 403))); err == nil {
		t.Fatal("accepted cloud rejection")
	}
}

func TestWechatStreamFramesPCMAndPollsPolishedFinal(t *testing.T) {
	key := []byte(wechatBootKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type uploaded struct {
		packets, uploads int
		polled           bool
		err              error
	}
	done := make(chan uploaded, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			done <- uploaded{err: err}
			return
		}
		defer ws.CloseNow()
		var got uploaded
		defer func() { done <- got }()
		dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(16000, 1))
		if err != nil {
			got.err = err
			return
		}
		var total uint64
		var voiceID string
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				got.err = err
				return
			}
			frame, err := parseWechat(raw)
			if err != nil {
				got.err = err
				return
			}
			req, err := parseWechat(frame.bytes(5))
			if err != nil {
				got.err = err
				return
			}
			plain, err := wechatDecrypt(key, req.bytes(6))
			if err != nil {
				got.err = err
				return
			}
			plain, err = snappy.Decode(nil, plain)
			if err != nil {
				got.err = err
				return
			}
			outer, _ := parseWechat(plain)
			inner, err := parseWechat(outer.bytes(1))
			if err != nil {
				got.err = err
				return
			}
			if voiceID == "" {
				voiceID = string(inner.bytes(2))
			}
			if string(inner.bytes(2)) != voiceID || inner.number(5) != 5 {
				got.err = fmt.Errorf("voice identity or codec changed")
				return
			}
			seq := inner.number(7)
			if seq == 0 {
				got.polled = inner.number(6) == 1 && len(inner.bytes(4)) == 0
			} else {
				got.uploads++
				if seq != uint64(got.uploads) {
					got.err = fmt.Errorf("sequence %d after upload %d", seq, got.uploads)
					return
				}
				audio := inner.bytes(4)
				total += uint64(len(audio))
				if inner.number(11) != total {
					got.err = fmt.Errorf("incorrect uploaded byte count")
					return
				}
				if seq == 1 {
					header := []byte("#!OPUS_RAW_V1\x02\x01\x00")
					if !bytes.HasPrefix(audio, header) {
						got.err = fmt.Errorf("missing Opus stream header")
						return
					}
					audio = audio[len(header):]
				}
				count := 0
				for len(audio) > 0 {
					if len(audio) < 2 {
						got.err = fmt.Errorf("truncated Opus length")
						return
					}
					n := int(binary.LittleEndian.Uint16(audio))
					audio = audio[2:]
					if n == 0 || n > len(audio) {
						got.err = fmt.Errorf("truncated Opus packet")
						return
					}
					samples, err := dec.DecodeInt16(audio[:n], make([]int16, 320))
					if err != nil || samples != 320 {
						got.err = fmt.Errorf("Opus decode: samples=%d, %v", samples, err)
						return
					}
					audio = audio[n:]
					count++
				}
				if count > 6 || (inner.number(6) == 0 && count != 6) {
					got.err = fmt.Errorf("unexpected batch size %d", count)
					return
				}
				got.packets += count
			}
			result := wechatMessage(nil).str(4, "中间结果")
			if seq == 0 {
				result = result.str(14, "整理后的定稿。")
			}
			cipher, _ := wechatEncrypt(key, snappy.Encode(nil, wechatMessage(nil).data(1, result)))
			response := wechatMessage(nil).num(2, 200).data(3, wechatMessage(nil).str(1, "Kb-CompressionType").str(2, "2")).data(5, cipher)
			if err := ws.Write(ctx, websocket.MessageBinary, wechatMessage(nil).data(4, response)); err != nil {
				got.err = err
				return
			}
			if seq == 0 {
				return
			}
		}
	}))
	defer server.Close()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	client := wechatClient{ws: ws, key: key, wechatIdentity: wechatIdentity{UIN: "123"}}
	// Chunk boundaries can split both a PCM frame and an individual sample.
	pcm := make([]byte, 7*640+112)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(i%3000))
	}
	audio := make(chan []byte, 3)
	audio <- pcm[:1]
	audio <- pcm[1:641]
	audio <- pcm[641:]
	close(audio)
	var partial string
	text, err := client.recognize(ctx, audio, func(s string) { partial = s })
	if err != nil || text != "整理后的定稿。" || partial != "中间结果" {
		t.Fatalf("transcript=%q, partial=%q, err=%v", text, partial, err)
	}
	got := <-done
	if got.err != nil || got.packets != 18 || got.uploads != 4 || !got.polled {
		t.Fatalf("uploaded=%+v", got)
	}
}

func TestWechatLive(t *testing.T) {
	file := os.Getenv("MIYA_WECHAT_TEST_WAV")
	if file == "" {
		t.Skip("set MIYA_WECHAT_TEST_WAV to send a test WAV to WeChat cloud")
	}
	pcm := readVoiceTestPCM(t, file)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir := os.Getenv("MIYA_WECHAT_TEST_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
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
		}
	}()
	text, err := recognizeWechat(ctx, dir, audio, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(strings.TrimSpace(text))) < 3 {
		t.Fatalf("missing transcript: %q", text)
	}
	if expected := os.Getenv("MIYA_WECHAT_TEST_EXPECT"); expected != "" && !strings.Contains(text, expected) {
		t.Fatalf("transcript %q does not contain %q", text, expected)
	}
	t.Logf("WeChat transcript: %s", text)
}
