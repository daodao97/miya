// Package voiceinput streams phone microphone audio to cloud transcription.
package voiceinput

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
)

// Protocol and client identity follow voicekey/crates/core/src/doubao.rs.
const doubaoURL = "wss://frontier-audio-ime-ws.doubao.com/ocean/api/v1/ws"
const doubaoKey = "OrnqKvSSrs"
const doubaoUA = "com.bytedance.android.doubaoime/100406010 (Linux; U; Android 16; en_US; Pixel 7 Pro; Build/BP2A.250605.031.A2; Cronet/TTNetVersion:94cf429a 2025-11-17 QuicVersion:1f89f732 2025-05-08)"

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func deviceID(ctx context.Context, dir string) (string, error) {
	file := filepath.Join(dir, "voice-doubao-device")
	if dir != "" {
		if b, err := os.ReadFile(file); err == nil {
			if did := strings.TrimSpace(string(b)); validDeviceID(did) {
				return did, nil
			}
		}
	}
	params := url.Values{}
	for k, v := range map[string]string{
		"aid": "401734", "app_name": "oime", "channel": "official", "version_code": "100406010", "version_name": "1.4.6",
		"manifest_version_code": "100406010", "update_version_code": "100406010", "package": "com.bytedance.android.doubaoime",
		"device_platform": "android", "os": "android", "os_api": "36", "os_version": "16", "device_type": "Pixel 7 Pro",
		"device_brand": "google", "device_model": "Pixel 7 Pro", "resolution": "1080*2340", "dpi": "420", "language": "zh",
		"timezone": "8", "access": "wifi", "rom": "BP2A.250605.031.A2", "rom_version": "BP2A.250605.031.A2",
	} {
		params.Set(k, v)
	}
	cdid := randomID()
	now := time.Now().UnixMilli()
	header := map[string]any{}
	for k, v := range params {
		header[k] = v[0]
	}
	for k, v := range map[string]any{"device_id": 0, "install_id": 0, "cdid": cdid, "openudid": randomID()[:8], "clientudid": randomID(), "region": "CN", "tz_name": "Asia/Shanghai", "tz_offset": 28800, "sim_region": "cn", "carrier_region": "cn", "cpu_abi": "arm64-v8a", "build_serial": "unknown", "not_request_sender": 0, "sig_hash": "", "google_aid": "", "mc": "", "serial_number": ""} {
		header[k] = v
	}
	body, _ := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": now})
	params.Set("ssmix", "a")
	params.Set("_rticket", fmt.Sprint(now))
	params.Set("cdid", cdid)
	params.Set("ac", "wifi")
	req, err := http.NewRequestWithContext(ctx, "POST", "https://log.snssdk.com/service/2/device_register/?"+params.Encode(), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", doubaoUA)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice registration HTTP %d", resp.StatusCode)
	}
	var result struct {
		ID     string      `json:"device_id_str"`
		Number json.Number `json:"device_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}
	if result.ID == "" {
		result.ID = result.Number.String()
	}
	if !validDeviceID(result.ID) {
		return "", errors.New("voice registration returned no device ID")
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0700); err == nil {
			_ = os.WriteFile(file, []byte(result.ID), 0600)
		}
	}
	return result.ID, nil
}

func validDeviceID(s string) bool {
	if s == "" || s == "0" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func pbBytes(b []byte, n uint64, value []byte) []byte {
	b = binary.AppendUvarint(b, n<<3|2)
	b = binary.AppendUvarint(b, uint64(len(value)))
	return append(b, value...)
}

func request(method, payload string, audio []byte, rid string, frame uint64) []byte {
	var b []byte
	if method != "TaskRequest" {
		b = pbBytes(b, 2, []byte(doubaoKey))
	}
	b = pbBytes(b, 3, []byte("ASR"))
	b = pbBytes(b, 5, []byte(method))
	if payload != "" {
		b = pbBytes(b, 6, []byte(payload))
	}
	if len(audio) > 0 {
		b = pbBytes(b, 7, audio)
	}
	b = pbBytes(b, 8, []byte(rid))
	if frame != 0 {
		b = binary.AppendUvarint(b, 9<<3)
		b = binary.AppendUvarint(b, frame)
	}
	return b
}

type response struct {
	event, message, result string
	status                 uint64
}

func parseResponse(b []byte) (response, error) {
	var r response
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return r, io.ErrUnexpectedEOF
		}
		b = b[n:]
		var data []byte
		var v uint64
		switch tag & 7 {
		case 0:
			v, n = binary.Uvarint(b)
			if n <= 0 {
				return r, io.ErrUnexpectedEOF
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return r, io.ErrUnexpectedEOF
			}
			b = b[8:]
		case 2:
			v, n = binary.Uvarint(b)
			if n <= 0 || v > uint64(len(b)-n) {
				return r, io.ErrUnexpectedEOF
			}
			b = b[n:]
			data, b = b[:int(v)], b[int(v):]
		case 5:
			if len(b) < 4 {
				return r, io.ErrUnexpectedEOF
			}
			b = b[4:]
		default:
			return r, errors.New("invalid voice response")
		}
		switch tag >> 3 {
		case 4:
			r.event = string(data)
		case 5:
			r.status = v
		case 6:
			r.message = string(data)
		case 7:
			r.result = string(data)
		}
	}
	return r, nil
}

func readResponse(ctx context.Context, ws *websocket.Conn) (response, error) {
	_, b, err := ws.Read(ctx)
	if err != nil {
		return response{}, err
	}
	r, err := parseResponse(b)
	if err == nil && (strings.HasSuffix(r.event, "Failed") || (r.status != 0 && r.status != 20000000)) {
		err = fmt.Errorf("Doubao %s (%d): %s", r.event, r.status, r.message)
	}
	return r, err
}

func expect(ctx context.Context, ws *websocket.Conn, event string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		r, err := readResponse(ctx, ws)
		if err != nil {
			return err
		}
		if r.event == event {
			return nil
		}
	}
}

type transcript struct {
	sentences map[int]string
	current   string
}

func (t *transcript) update(raw string) error {
	var obj struct {
		Results []struct {
			Text    string `json:"text"`
			Index   *int   `json:"index"`
			Interim *bool  `json:"is_interim"`
			VAD     bool   `json:"is_vad_finished"`
			Extra   struct {
				Final bool `json:"nonstream_result"`
			} `json:"extra"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return err
	}
	if len(obj.Results) == 0 {
		return nil
	}
	indexed := false
	for _, r := range obj.Results {
		indexed = indexed || r.Index != nil
	}
	if !indexed {
		t.sentences = nil
		t.current = obj.Results[0].Text
		return nil
	}
	if t.sentences == nil {
		t.sentences = map[int]string{}
	}
	for _, r := range obj.Results {
		if r.Text == "" {
			continue
		}
		if r.Extra.Final || (r.Interim != nil && !*r.Interim && r.VAD) {
			i := len(t.sentences)
			if r.Index != nil {
				i = *r.Index
			}
			t.sentences[i] = r.Text
			t.current = ""
		} else {
			t.current = r.Text
		}
	}
	return nil
}

func (t *transcript) text() string {
	keys := make([]int, 0, len(t.sentences))
	for k := range t.sentences {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, t.sentences[k])
	}
	parts = append(parts, t.current)
	var out []rune
	cjk := func(r rune) bool {
		return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || (r >= 0xff00 && r <= 0xffef)
	}
	for _, s := range parts {
		p := []rune(strings.TrimSpace(s))
		if len(p) == 0 {
			continue
		}
		if len(out) > 0 && !cjk(out[len(out)-1]) && !cjk(p[0]) {
			out = append(out, ' ')
		}
		out = append(out, p...)
	}
	return string(out)
}

// recognize consumes 16 kHz mono signed little-endian PCM. Audio stays in
// bounded memory and is never written to disk or sent through the desktop.
func recognize(ctx context.Context, dir string, audio <-chan []byte, partial func(string)) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	did, err := deviceID(ctx, dir)
	if err != nil {
		return "", err
	}
	ws, resp, err := websocket.Dial(ctx, doubaoURL+"?aid=401734&device_id="+url.QueryEscape(did), &websocket.DialOptions{HTTPHeader: http.Header{"User-Agent": {doubaoUA}, "Proto-Version": {"v2"}, "X-Custom-Keepalive": {"true"}}})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return "", err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	rid := randomID()
	send := func(method, payload string, b []byte, frame uint64) error {
		return ws.Write(ctx, websocket.MessageBinary, request(method, payload, b, rid, frame))
	}
	config, _ := json.Marshal(map[string]any{"audio_info": map[string]any{"channel": 1, "format": "pcm", "sample_rate": 16000, "bits": 16}, "enable_punctuation": true, "enable_speech_rejection": true, "extra": map[string]any{"app_name": "oime", "did": did, "enable_asr_threepass": false, "enable_asr_twopass": true, "input_mode": "stream"}})
	if err = send("StartTask", "", nil, 0); err == nil {
		err = expect(ctx, ws, "TaskStarted")
	}
	if err == nil {
		err = send("StartSession", string(config), nil, 0)
	}
	if err == nil {
		err = expect(ctx, ws, "SessionStarted")
	}
	if err != nil {
		if dir != "" {
			_ = os.Remove(filepath.Join(dir, "voice-doubao-device"))
		}
		return "", err
	}
	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 1)
	readerExited := make(chan struct{})
	go func() {
		defer close(readerExited)
		var t transcript
		for {
			r, err := readResponse(ctx, ws)
			if err == nil && r.result != "" {
				err = t.update(r.result)
				if err == nil {
					partial(t.text())
				}
			}
			if err != nil || r.event == "SessionFinished" {
				done <- outcome{t.text(), err}
				return
			}
		}
	}()
	// No callback may outlive recognize: Session closes Results on return.
	defer func() { cancel(); ws.CloseNow(); <-readerExited }()
	ts := time.Now().UnixMilli()
	samples := 0
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case r := <-done:
			return r.text, r.err
		case b, ok := <-audio:
			meta := fmt.Sprintf(`{"extra":{},"timestamp_ms":%d}`, ts+int64(samples/16))
			if !ok {
				if err = send("TaskRequest", meta, nil, 9); err == nil {
					err = send("FinishSession", "", nil, 0)
				}
				if err != nil {
					return "", err
				}
				timer := time.NewTimer(15 * time.Second)
				defer timer.Stop()
				select {
				case r := <-done:
					return r.text, r.err
				case <-ctx.Done():
					return "", ctx.Err()
				case <-timer.C:
					return "", errors.New("voice finalization timed out")
				}
			}
			frame := uint64(3)
			if samples == 0 {
				frame = 1
			}
			if err = send("TaskRequest", meta, b, frame); err != nil {
				return "", err
			}
			samples += len(b) / 2
		}
	}
}
