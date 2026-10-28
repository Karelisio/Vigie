package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LogLine est une ligne de log d'un conteneur.
type LogLine struct {
	TS     int64  `json:"ts"` // millisecondes Unix
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

const maxLineLen = 8 << 10

// Logs lit les logs d'un conteneur et appelle fn pour chaque ligne.
// Avec follow, la fonction bloque jusqu'à l'annulation du contexte.
func (c *Client) Logs(ctx context.Context, id string, tail int, follow bool, tty bool, fn func(LogLine)) error {
	q := url.Values{
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
		"tail":       {strconv.Itoa(tail)},
	}
	if follow {
		q.Set("follow", "1")
	}
	cl := c.http
	if follow {
		cl = c.stream
	}
	resp, err := c.req(ctx, cl, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if tty || resp.Header.Get("Content-Type") == "application/vnd.docker.raw-stream" {
		return scanLines(resp.Body, "stdout", fn)
	}
	return demux(resp.Body, fn)
}

// demux décode le flux multiplexé (en-tête de 8 octets par trame).
func demux(r io.Reader, fn func(LogLine)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	hdr := make([]byte, 8)
	partial := map[string]*strings.Builder{"stdout": {}, "stderr": {}}
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			return eofNil(err)
		}
		stream := "stdout"
		if hdr[0] == 2 {
			stream = "stderr"
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return eofNil(err)
		}
		sb := partial[stream]
		for len(buf) > 0 {
			i := indexByte(buf, '\n')
			if i < 0 {
				if sb.Len() < maxLineLen {
					sb.Write(buf)
				}
				break
			}
			sb.Write(buf[:i])
			emit(sb.String(), stream, fn)
			sb.Reset()
			buf = buf[i+1:]
		}
	}
}

func scanLines(r io.Reader, stream string, fn func(LogLine)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		emit(sc.Text(), stream, fn)
	}
	return eofNil(sc.Err())
}

func emit(raw, stream string, fn func(LogLine)) {
	raw = strings.TrimRight(raw, "\r")
	ts := time.Now()
	if i := strings.IndexByte(raw, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, raw[:i]); err == nil {
			ts = t
			raw = raw[i+1:]
		}
	}
	raw = ansi.ReplaceAllString(raw, "")
	if len(raw) > maxLineLen {
		raw = raw[:maxLineLen] + "…"
	}
	fn(LogLine{TS: ts.UnixMilli(), Stream: stream, Line: raw})
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func eofNil(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil
	}
	return err
}

// Event est un événement Docker de type conteneur.
type Event struct {
	Action string `json:"Action"`
	Status string `json:"status"`
	ID     string `json:"id"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time     int64 `json:"time"`
	TimeNano int64 `json:"timeNano"`
}

func (e Event) Name() string { return e.Actor.Attributes["name"] }

// Events s'abonne aux événements conteneur et appelle fn pour chacun,
// jusqu'à l'annulation du contexte ou une erreur de flux.
func (c *Client) Events(ctx context.Context, fn func(Event)) error {
	filters, _ := json.Marshal(map[string][]string{"type": {"container"}})
	resp, err := c.req(ctx, c.stream, http.MethodGet, "/events", url.Values{"filters": {string(filters)}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if e.Action == "" {
			e.Action = e.Status
		}
		fn(e)
	}
}
