package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 simule un stockage compatible S3 (adresses « path-style ») et vérifie la signature de
// chaque requête avec sa propre implémentation de SigV4, indépendante de celle du paquet.
type fakeS3 struct {
	t                      *testing.T
	access, secret, region string
	bucket                 string
	minPart                int // taille minimale d'une partie (hors dernière)
	pageSize               int // objets par page de ListObjectsV2

	mu       sync.Mutex
	objects  map[string][]byte
	modified map[string]time.Time
	uploads  map[string]map[int][]byte
	seq      int
	maxBody  int            // plus gros corps reçu
	failOnce map[string]int // « méthode chemin?requête » → nombre d'échecs 500 à simuler
	log      []string
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	f := &fakeS3{
		t: t, access: "AKTEST", secret: "secret/TEST+key", region: "eu-paris-1", bucket: "sauvegardes",
		minPart: 1, pageSize: 1000,
		objects: map[string][]byte{}, modified: map[string]time.Time{}, uploads: map[string]map[int][]byte{},
		failOnce: map[string]int{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) config(endpoint string) (Config, secrets) {
	c := defaultConfig()
	c.Endpoint, c.Region, c.Bucket = endpoint, f.region, f.bucket
	return c, secrets{AccessKey: f.access, SecretKey: f.secret}
}

func (f *fakeS3) put(key string, body []byte, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key], f.modified[key] = body, at
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (f *fakeS3) object(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objects[key]
}

func s3Fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>simulé</Message></Error>`, code)
}

// encode : encodage URI de SigV4, réécrit ici pour vérifier celui du client.
func encode(s string, slash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 || slash && c == '/' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func mac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	io.WriteString(h, s)
	return h.Sum(nil)
}

// verify contrôle la signature d'une requête telle que le serveur la reçoit.
func (f *fakeS3) verify(r *http.Request, body []byte) string {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		return "AccessDenied"
	}
	fields := map[string]string{}
	for _, part := range strings.Split(rest, ", ") {
		k, v, _ := strings.Cut(part, "=")
		fields[k] = v
	}
	scope := strings.Split(fields["Credential"], "/")
	if len(scope) != 5 || scope[0] != f.access {
		return "InvalidAccessKeyId"
	}
	date := r.Header.Get("X-Amz-Date")
	if scope[2] != f.region || scope[3] != "s3" || scope[4] != "aws4_request" || !strings.HasPrefix(date, scope[1]) {
		return "AuthorizationHeaderMalformed"
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) {
		return "XAmzContentSHA256Mismatch"
	}
	signed := strings.Split(fields["SignedHeaders"], ";")
	need := map[string]bool{"host": true, "x-amz-date": true, "x-amz-content-sha256": true}
	var hdrs strings.Builder
	for _, h := range signed {
		delete(need, h)
		v := r.Header.Get(h)
		if h == "host" {
			v = r.Host
		}
		hdrs.WriteString(h + ":" + strings.TrimSpace(v) + "\n")
	}
	if len(need) > 0 {
		return "AccessDenied"
	}
	var pairs []string
	if r.URL.RawQuery != "" {
		for _, kv := range strings.Split(r.URL.RawQuery, "&") {
			k, v, _ := strings.Cut(kv, "=")
			k, _ = url.QueryUnescape(k)
			v, _ = url.QueryUnescape(v)
			pairs = append(pairs, encode(k, false)+"="+encode(v, false))
		}
	}
	sort.Strings(pairs) // pas de nom de paramètre préfixe d'un autre dans ces tests
	creq := strings.Join([]string{
		r.Method, encode(r.URL.Path, true), strings.Join(pairs, "&"), hdrs.String(),
		fields["SignedHeaders"], hex.EncodeToString(sum[:]),
	}, "\n")
	crsum := sha256.Sum256([]byte(creq))
	toSign := "AWS4-HMAC-SHA256\n" + date + "\n" + strings.Join(scope[1:], "/") + "\n" + hex.EncodeToString(crsum[:])
	key := mac(mac(mac(mac([]byte("AWS4"+f.secret), scope[1]), scope[2]), scope[3]), "aws4_request")
	if hex.EncodeToString(mac(key, toSign)) != fields["Signature"] {
		return "SignatureDoesNotMatch"
	}
	return ""
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.ContentLength != int64(len(body)) {
		f.t.Errorf("Content-Length %d pour un corps de %d octets", r.ContentLength, len(body))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(body) > f.maxBody {
		f.maxBody = len(body)
	}
	q := r.URL.Query()
	f.log = append(f.log, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	if code := f.verify(r, body); code != "" {
		s3Fail(w, http.StatusForbidden, code)
		return
	}
	failKey := r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
	if f.failOnce[failKey] > 0 {
		f.failOnce[failKey]--
		s3Fail(w, http.StatusServiceUnavailable, "SlowDown")
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket != f.bucket {
		s3Fail(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	switch {
	case r.Method == http.MethodGet && key == "" && q.Get("list-type") == "2":
		f.listV2(w, q)
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.seq++
		id := "up-" + strconv.Itoa(f.seq)
		f.uploads[id] = map[int][]byte{}
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, bucket, key, id)
	case r.Method == http.MethodPut && q.Has("uploadId"):
		parts, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			s3Fail(w, http.StatusNotFound, "NoSuchUpload")
			return
		}
		n, _ := strconv.Atoi(q.Get("partNumber"))
		parts[n] = body
		w.Header().Set("ETag", fmt.Sprintf(`"etag-%d-%d"`, n, len(body)))
	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.complete(w, key, q.Get("uploadId"), body)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		f.objects[key], f.modified[key] = body, time.Now()
		w.Header().Set("ETag", `"simple"`)
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		s3Fail(w, http.StatusNotImplemented, "NotImplemented")
	}
}

func (f *fakeS3) complete(w http.ResponseWriter, key, id string, body []byte) {
	parts, ok := f.uploads[id]
	if !ok {
		s3Fail(w, http.StatusNotFound, "NoSuchUpload")
		return
	}
	var req struct {
		Parts []struct {
			Number int    `xml:"PartNumber"`
			ETag   string `xml:"ETag"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) != len(parts) {
		s3Fail(w, http.StatusBadRequest, "MalformedXML")
		return
	}
	var all []byte
	for i, p := range req.Parts {
		data := parts[p.Number]
		if p.Number != i+1 || p.ETag != fmt.Sprintf(`"etag-%d-%d"`, p.Number, len(data)) {
			s3Fail(w, http.StatusBadRequest, "InvalidPart")
			return
		}
		if i < len(req.Parts)-1 && len(data) < f.minPart {
			s3Fail(w, http.StatusBadRequest, "EntityTooSmall")
			return
		}
		all = append(all, data...)
	}
	delete(f.uploads, id)
	f.objects[key], f.modified[key] = all, time.Now()
	fmt.Fprintf(w, `<CompleteMultipartUploadResult><Key>%s</Key><ETag>"final"</ETag></CompleteMultipartUploadResult>`, key)
}

func (f *fakeS3) listV2(w http.ResponseWriter, q url.Values) {
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, q.Get("prefix")) && k > q.Get("continuation-token") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	truncated := len(keys) > f.pageSize
	if truncated {
		keys = keys[:f.pageSize]
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>` + f.bucket + `</Name>`)
	for _, k := range keys {
		fmt.Fprintf(&b, `<Contents><Key>%s</Key><LastModified>%s</LastModified><Size>%d</Size></Contents>`,
			k, f.modified[k].UTC().Format(time.RFC3339), len(f.objects[k]))
	}
	fmt.Fprintf(&b, `<KeyCount>%d</KeyCount><IsTruncated>%t</IsTruncated>`, len(keys), truncated)
	if truncated {
		fmt.Fprintf(&b, `<NextContinuationToken>%s</NextContinuationToken>`, keys[len(keys)-1])
	}
	b.WriteString(`</ListBucketResult>`)
	io.WriteString(w, b.String())
}
