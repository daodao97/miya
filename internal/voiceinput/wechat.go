package voiceinput

// WeChat Input's cloud protocol follows voicekey/crates/core/src/wetype.rs.
// Capture stays platform-specific; framing, Opus, encryption and transport
// run in Go, so the same client can be used by both phone platforms.
import (
	"context"
	"crypto/aes"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/klauspost/compress/snappy"
	"github.com/thesyncim/gopus"
)

const (
	wechatHost    = "wetype.weixin.qq.com"
	wechatSignKey = "zN7rB3bL4pO8jW1o"
	wechatBootKey = "D4Y5U3Y2M0C0T7N4P1P7O2N6E1I2Y1U6"
	wechatVersion = "2.2.3(657)"
	wechatModel   = "Mac16,12"
	wechatDH      = uint64(2147483646)
	wechatUIN     = uint64(0x7ffffdfd)
	wechatNotify  = uint64(8074)
	wechatVoice   = uint64(4548)
	wechatLimit   = 1 << 20
)

type wechatIdentity struct {
	Device string `json:"device"`
	UIN    string `json:"uin"`
}

type wechatClient struct {
	ws *websocket.Conn
	wechatIdentity
	key           []byte
	task          uint64
	server, token string
}

type wechatReply struct {
	status  uint64
	headers http.Header
	body    []byte
}

type wechatMessage []byte

func (b wechatMessage) data(field uint64, value []byte) wechatMessage {
	return pbBytes(b, field, value)
}
func (b wechatMessage) str(field uint64, value string) wechatMessage {
	return b.data(field, []byte(value))
}
func (b wechatMessage) num(field, value uint64) wechatMessage {
	b = binary.AppendUvarint(b, field<<3)
	return binary.AppendUvarint(b, value)
}
func (b wechatMessage) header(key, value string) wechatMessage {
	return b.data(5, wechatMessage(nil).str(1, key).str(2, value))
}

type wechatField struct {
	number uint64
	value  uint64
	data   []byte
}
type wechatFields []wechatField

func parseWechat(b []byte) (wechatFields, error) {
	var fields wechatFields
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid WeChat protobuf tag")
		}
		b = b[n:]
		f := wechatField{number: tag >> 3}
		switch tag & 7 {
		case 0:
			f.value, n = binary.Uvarint(b)
			if n <= 0 {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[n:]
		case 1, 5:
			n = 8
			if tag&7 == 5 {
				n = 4
			}
			if len(b) < n {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[n:]
		case 2:
			length, n := binary.Uvarint(b)
			if n <= 0 || length > uint64(len(b)-n) {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[n:]
			f.data, b = b[:int(length)], b[int(length):]
		default:
			return nil, errors.New("unsupported WeChat protobuf field")
		}
		fields = append(fields, f)
	}
	return fields, nil
}
func (f wechatFields) bytes(n uint64) []byte {
	for _, v := range f {
		if v.number == n {
			return v.data
		}
	}
	return nil
}
func (f wechatFields) number(n uint64) uint64 {
	for _, v := range f {
		if v.number == n {
			return v.value
		}
	}
	return 0
}

func wechatEncrypt(key, data []byte) ([]byte, error) {
	cipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(data)%aes.BlockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	for i := 0; i < len(out); i += aes.BlockSize {
		cipher.Encrypt(out[i:i+aes.BlockSize], out[i:i+aes.BlockSize])
	}
	return out, nil
}

func wechatDecrypt(key, data []byte) ([]byte, error) {
	cipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid WeChat encrypted payload")
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		cipher.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	padding := int(out[len(out)-1])
	if padding == 0 || padding > aes.BlockSize {
		return nil, errors.New("invalid WeChat encryption padding")
	}
	for _, v := range out[len(out)-padding:] {
		if int(v) != padding {
			return nil, errors.New("invalid WeChat encryption padding")
		}
	}
	return out[:len(out)-padding], nil
}

// This legacy curve is required by WeChat Input's wire protocol. CurveParams
// implements its a=-3 arithmetic; reject invalid server points before ECDH.
func wechatCurve() *elliptic.CurveParams {
	hexInt := func(s string) *big.Int { n, _ := new(big.Int).SetString(s, 16); return n }
	return &elliptic.CurveParams{
		Name: "secp128r1", BitSize: 128,
		P: hexInt("FFFFFFFDFFFFFFFFFFFFFFFFFFFFFFFF"), N: hexInt("FFFFFFFE0000000075A30D1B9038A115"),
		B:  hexInt("E87579C11079F43DD824993C2CEE5ED3"),
		Gx: hexInt("161FF7528B899B2D0C28607CA52C5B86"), Gy: hexInt("CF5AC8395BAFEB13C02DA292DDED7A83"),
	}
}

func wechatSharedKey(private []byte, public string) ([]byte, error) {
	b, err := hex.DecodeString(public)
	if err != nil || len(b) != 33 || b[0] != 4 {
		return nil, errors.New("invalid WeChat server public key")
	}
	curve := wechatCurve()
	x, y := new(big.Int).SetBytes(b[1:17]), new(big.Int).SetBytes(b[17:])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("invalid WeChat server public key")
	}
	x, y = curve.ScalarMult(x, y, private)
	if x.Sign() == 0 && y.Sign() == 0 {
		return nil, errors.New("invalid WeChat shared key")
	}
	return []byte(fmt.Sprintf("%032X", x)), nil
}

func wechatMD5(b []byte) string {
	digest := md5.Sum(b)
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func (c *wechatClient) roundTrip(ctx context.Context, path string, body []byte, command uint64, compression, token string) (wechatReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dh, gen := command == wechatDH, command == wechatUIN
	task := command
	if !dh && !gen {
		c.task++
		task = c.task
	}
	trace, timestamp, digest := randomID()[:16], strconv.FormatInt(time.Now().UnixMilli(), 10), wechatMD5(body)
	cmd, taskID := strconv.FormatUint(command, 10), strconv.FormatUint(task, 10)
	parts := []string{"5", wechatVersion, "2"}
	switch {
	case dh:
		parts = append(parts, timestamp, digest, trace, cmd)
	case gen:
		parts = append(parts, timestamp, digest, token, trace, cmd)
	default:
		parts = append(parts, cmd, "0", timestamp, digest, c.UIN, trace, taskID)
	}
	sign := sha256.Sum256([]byte(strings.Join(parts, "") + wechatSignKey))
	headers := [][2]string{
		{"Kb-Uin", c.UIN}, {"Kb-OsType", "5"}, {"Kb-Version", wechatVersion}, {"Kb-SystemVersion", "27.0.0"},
		{"Kb-PackageType", "3"}, {"Use_DebugNet", "0"}, {"Kb-TimeStamp", timestamp}, {"Kb-BodyMd5", digest},
		{"Kb-TraceId", trace}, {"Kb-TaskId", taskID}, {"Kb-Scene", "2"}, {"Kb-Sign", strings.ToUpper(hex.EncodeToString(sign[:]))},
		{"Content-Length", strconv.Itoa(len(body))}, {"Kb-CompressionType", compression}, {"Content-Type", "application/octet-stream"}, {"HOST", wechatHost},
	}
	if token != "" {
		headers = append(headers, [2]string{"Kb-GenUinToken", token})
	}
	if dh || gen {
		headers = append(headers, [2]string{"Kb-DeviceCodeRestrictionV2", "1"})
	}
	if !dh {
		headers = append(headers, [2]string{"Kb-CmdId", cmd}, [2]string{"Kb-SubCmdId", "0"})
		if len(c.key) >= 4 {
			headers = append(headers, [2]string{"Kb-SharedKeySuffix", string(c.key[len(c.key)-4:])})
		}
	}
	req := wechatMessage(nil).str(1, "POST").str(3, path).str(4, "")
	for _, h := range headers {
		req = req.header(h[0], h[1])
	}
	req = req.data(6, body)
	frame := wechatMessage(nil).num(1, 0).num(2, 0).num(3, task).data(5, req)
	if err := c.ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
		return wechatReply{}, err
	}
	_, raw, err := c.ws.Read(ctx)
	if err != nil {
		return wechatReply{}, err
	}
	return parseWechatReply(raw)
}

func parseWechatReply(raw []byte) (wechatReply, error) {
	frame, err := parseWechat(raw)
	if err != nil {
		return wechatReply{}, err
	}
	fields, err := parseWechat(frame.bytes(4))
	if err != nil {
		return wechatReply{}, err
	}
	r := wechatReply{status: fields.number(2), body: fields.bytes(5), headers: make(http.Header)}
	for _, field := range fields {
		if field.number == 5 || field.data == nil {
			continue
		}
		kv, err := parseWechat(field.data)
		if err == nil && len(kv) >= 2 && kv[0].data != nil && kv[1].data != nil {
			r.headers.Set(string(kv[0].data), string(kv[1].data))
		}
	}
	if r.status != http.StatusOK {
		return r, fmt.Errorf("WeChat cloud HTTP %d", r.status)
	}
	return r, nil
}

func (c *wechatClient) request(ctx context.Context, path string, plain, key []byte, command uint64, token string) ([]byte, error) {
	body, err := wechatEncrypt(key, plain)
	if err != nil {
		return nil, err
	}
	r, err := c.roundTrip(ctx, path, body, command, "1", token)
	if err != nil {
		return nil, err
	}
	return wechatDecrypt(key, r.body)
}

func (c *wechatClient) exchangeKey(ctx context.Context, register bool) error {
	curve := wechatCurve()
	private, x, y, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		return err
	}
	public := fmt.Sprintf("04%032X%032X", x, y)
	req := wechatMessage(nil).str(1, public).str(2, public)
	if register {
		req = req.num(3, 1)
	}
	req = req.str(4, c.Device)
	if !register {
		uin, _ := strconv.ParseUint(c.UIN, 10, 64)
		req = req.num(5, uin)
	}
	req = req.str(7, "").str(8, wechatModel).str(9, "")
	raw, err := c.request(ctx, "/oauth_pubkey_v2", req, []byte(wechatBootKey), wechatDH, "")
	if err != nil {
		return err
	}
	f, err := parseWechat(raw)
	if err != nil {
		return err
	}
	c.server, c.token = string(f.bytes(2)), string(f.bytes(4))
	if register && c.token == "" {
		return errors.New("WeChat cloud did not issue registration token")
	}
	c.key, err = wechatSharedKey(private, c.server)
	return err
}

func (c *wechatClient) register(ctx context.Context) error {
	c.UIN = "0"
	body := "MAC" + strings.Repeat("0", 17-len(wechatModel)) + wechatModel + randomID()[:12]
	c.Device = body + wechatMD5([]byte(body+wechatSignKey))
	if err := c.exchangeKey(ctx, true); err != nil {
		return err
	}
	req := wechatMessage(nil).str(1, c.Device).str(2, c.server).str(4, c.token)
	raw, err := c.request(ctx, "/gen_uin_v2", req, c.key, wechatUIN, c.token)
	if err != nil {
		return err
	}
	f, err := parseWechat(raw)
	if err != nil {
		return err
	}
	if f.number(2) == 0 {
		return errors.New("WeChat cloud did not issue UIN")
	}
	c.UIN = strconv.FormatUint(f.number(2), 10)
	return nil
}

func openWechat(ctx context.Context, dir string) (*wechatClient, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "wss://"+wechatHost+"/", &websocket.DialOptions{Subprotocols: []string{"wxws_pb"}})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, err
	}
	ws.SetReadLimit(wechatLimit)
	c := &wechatClient{ws: ws, wechatIdentity: wechatIdentity{UIN: "0"}}
	if err = c.handshake(ctx, dir); err != nil {
		ws.CloseNow()
		return nil, err
	}
	return c, nil
}

func (c *wechatClient) handshake(ctx context.Context, dir string) error {
	if _, err := c.roundTrip(ctx, "/timestamp", nil, 0, "1", ""); err != nil {
		return err
	}
	file := filepath.Join(dir, "voice-wechat-device.json")
	var saved wechatIdentity
	if dir != "" {
		b, _ := os.ReadFile(file)
		_ = json.Unmarshal(b, &saved)
	}
	uin, err := strconv.ParseUint(saved.UIN, 10, 64)
	if err == nil && uin != 0 && len(saved.Device) == 64 {
		c.wechatIdentity = saved
		err = c.exchangeKey(ctx, false)
	} else {
		err = errors.New("no saved WeChat identity")
	}
	if err != nil {
		if err = c.register(ctx); err != nil {
			return err
		}
	}
	notify, err := wechatEncrypt(c.key, nil)
	if err != nil {
		return err
	}
	if _, err = c.roundTrip(ctx, "/api_v2", notify, wechatNotify, "1", ""); err != nil {
		return err
	}
	if dir != "" && saved != c.wechatIdentity {
		// Only the anonymous device identity is persisted, never microphone audio.
		if err := os.MkdirAll(dir, 0700); err == nil {
			b, _ := json.Marshal(c.wechatIdentity)
			_ = os.WriteFile(file, b, 0600)
		}
	}
	return nil
}

func (c *wechatClient) voice(ctx context.Context, voice string, opus []byte, seq, total uint64, end bool) (text, polished string, err error) {
	inner := wechatMessage(nil).str(2, voice)
	if len(opus) > 0 {
		inner = inner.data(4, opus)
	}
	inner = inner.num(5, 5)
	if end {
		inner = inner.num(6, 1)
	}
	inner = inner.num(7, seq)
	if total > 0 {
		inner = inner.num(11, total)
	}
	inner = inner.num(22, 1).num(23, 1).num(24, 1)
	body, err := wechatEncrypt(c.key, snappy.Encode(nil, wechatMessage(nil).data(1, inner)))
	if err != nil {
		return "", "", err
	}
	r, err := c.roundTrip(ctx, "/api_v2", body, wechatVoice, "2", "")
	if err != nil {
		return "", "", err
	}
	return decodeWechatVoice(c.key, r)
}

func decodeWechatVoice(key []byte, r wechatReply) (text, polished string, err error) {
	plain, err := wechatDecrypt(key, r.body)
	if err != nil {
		return "", "", err
	}
	if r.headers.Get("Kb-CompressionType") == "2" || r.headers.Get("CompressionType") == "2" {
		n, err := snappy.DecodedLen(plain)
		if err != nil || n > wechatLimit {
			return "", "", errors.New("invalid WeChat compressed response")
		}
		plain, err = snappy.Decode(nil, plain)
		if err != nil {
			return "", "", err
		}
	}
	f, err := parseWechat(plain)
	if err != nil {
		return "", "", err
	}
	if inner := f.bytes(1); inner != nil {
		f, err = parseWechat(inner)
		if err != nil {
			return "", "", err
		}
	}
	return string(f.bytes(4)), string(f.bytes(14)), nil
}

// recognizeWechat consumes the same 16 kHz mono PCM as the microphone bridge.
// Each request contains six 20 ms Opus frames, paced like voicekey's stream.
func recognizeWechat(ctx context.Context, dir string, audio <-chan []byte, partial func(string)) (string, error) {
	c, err := openWechat(ctx, dir)
	if err != nil {
		return "", err
	}
	defer c.ws.CloseNow()
	return c.recognize(ctx, audio, partial)
}

func (c *wechatClient) recognize(ctx context.Context, audio <-chan []byte, partial func(string)) (string, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 16000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		return "", err
	}
	if err := enc.SetBitrate(64000); err != nil {
		return "", err
	}
	if err := enc.SetComplexity(9); err != nil {
		return "", err
	}
	voice := randomID()
	var seq, total uint64
	var text, polished string
	send := func(payload []byte, sequence uint64, end bool) error {
		next, final, err := c.voice(ctx, voice, payload, sequence, total, end)
		if err != nil {
			return err
		}
		if next != "" {
			text = next
			partial(text)
		}
		if final != "" {
			polished = final
		}
		return nil
	}
	var batch []byte
	packets := 0
	upload := func(end bool) error {
		seq++
		payload := batch
		if seq == 1 {
			payload = append([]byte("#!OPUS_RAW_V1\x02\x01\x00"), batch...)
		}
		total += uint64(len(payload))
		if err := send(payload, seq, end); err != nil {
			return err
		}
		batch, packets = batch[:0], 0
		return nil
	}
	pcm := make([]int16, 320)
	packet := make([]byte, 4000)
	var next time.Time
	encode := func(frame []byte) error {
		if next.IsZero() {
			next = time.Now()
		}
		if err := waitWechat(ctx, time.Until(next)); err != nil {
			return err
		}
		// Keep audio time independent of HTTP response latency. Restarting
		// this clock after each upload would make long recordings accumulate
		// a network round trip of delay for every six frames.
		next = next.Add(20 * time.Millisecond)
		clear(pcm)
		for i := 0; i+1 < len(frame); i += 2 {
			pcm[i/2] = int16(binary.LittleEndian.Uint16(frame[i:]))
		}
		n, err := enc.EncodeInt16(pcm, packet)
		if err != nil {
			return err
		}
		batch = binary.LittleEndian.AppendUint16(batch, uint16(n))
		batch = append(batch, packet[:n]...)
		packets++
		if packets == 6 {
			return upload(false)
		}
		return nil
	}
	for range 10 {
		if err := encode(nil); err != nil {
			return "", err
		}
	}
	var pending []byte
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case b, ok := <-audio:
			pending = append(pending, b...)
			for len(pending) >= 640 {
				if err := encode(pending[:640]); err != nil {
					return "", err
				}
				pending = pending[640:]
			}
			if ok {
				continue
			}
			if len(pending)%2 != 0 {
				return "", errors.New("incomplete microphone PCM sample")
			}
			if len(pending) > 0 {
				if err := encode(pending); err != nil {
					return "", err
				}
			}
			if err := upload(true); err != nil {
				return "", err
			}
			for i := 0; i < 8 && polished == ""; i++ {
				if err := waitWechat(ctx, 250*time.Millisecond); err != nil {
					return "", err
				}
				if err := send(nil, 0, true); err != nil {
					return "", err
				}
			}
			if polished != "" {
				return polished, nil
			}
			return text, nil
		}
	}
}

func waitWechat(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
