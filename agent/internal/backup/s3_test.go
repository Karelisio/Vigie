package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func newClient(t *testing.T, f *fakeS3, endpoint string) *s3Client {
	t.Helper()
	cfg, sec := f.config(endpoint)
	cl, err := newS3(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	cl.retryGap = time.Millisecond
	return cl
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestUploadSmallObject(t *testing.T) {
	f, srv := newFakeS3(t)
	cl := newClient(t, f, srv.URL)
	data := []byte("petit contenu")
	var sent int64
	n, err := cl.Upload(context.Background(), "vigie/x/2026-10-28_0330.tar.gz", bytes.NewReader(data), make([]byte, 1024), func(v int64) { sent = v })
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(data)) || sent != n || !bytes.Equal(f.object("vigie/x/2026-10-28_0330.tar.gz"), data) {
		t.Fatalf("envoi simple : %d octets, %d signalés, objets %v", n, sent, f.keys())
	}
	if len(f.log) != 1 || !strings.HasPrefix(f.log[0], "PUT ") {
		t.Fatalf("une seule requête PUT attendue : %v", f.log)
	}
}

func TestUploadMultipart(t *testing.T) {
	for _, size := range []int{2500, 3000} { // parties de 1000 octets : 3 (dernière plus courte), puis 3 exactement
		f, srv := newFakeS3(t)
		f.minPart = 1000
		cl := newClient(t, f, srv.URL)
		data := randomBytes(size)
		n, err := cl.Upload(context.Background(), "a/b.tar.gz", bytes.NewReader(data), make([]byte, 1000), func(int64) {})
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(size) || !bytes.Equal(f.object("a/b.tar.gz"), data) {
			t.Fatalf("%d octets : objet reconstitué incorrect (%d)", size, len(f.object("a/b.tar.gz")))
		}
		if f.maxBody > 1000 {
			t.Errorf("corps de %d octets : la mémoire doit rester bornée à une partie", f.maxBody)
		}
		parts := 0
		for _, l := range f.log {
			if strings.Contains(l, "partNumber=") {
				parts++
			}
		}
		if parts != 3 || len(f.uploads) != 0 {
			t.Errorf("%d octets : %d parties envoyées, envois en cours %v", size, parts, f.uploads)
		}
	}
}

func TestUploadRetriesPart(t *testing.T) {
	f, srv := newFakeS3(t)
	f.failOnce["PUT /sauvegardes/k?partNumber=2&uploadId=up-1"] = 2
	cl := newClient(t, f, srv.URL)
	data := randomBytes(2500)
	if _, err := cl.Upload(context.Background(), "k", bytes.NewReader(data), make([]byte, 1000), func(int64) {}); err != nil {
		t.Fatalf("deux échecs passagers puis réussite : %v", err)
	}
	if !bytes.Equal(f.object("k"), data) {
		t.Fatal("objet incorrect après nouveaux essais")
	}
}

type failingReader struct {
	r     io.Reader
	after int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("flux interrompu")
	}
	p = p[:min(len(p), f.after)]
	n, err := f.r.Read(p)
	f.after -= n
	return n, err
}

func TestUploadAbortsOnError(t *testing.T) {
	f, srv := newFakeS3(t)
	cl := newClient(t, f, srv.URL)
	r := &failingReader{r: bytes.NewReader(randomBytes(5000)), after: 2500}
	_, err := cl.Upload(context.Background(), "k", r, make([]byte, 1000), func(int64) {})
	if err == nil || !strings.Contains(err.Error(), "flux interrompu") {
		t.Fatalf("erreur du flux attendue : %v", err)
	}
	if len(f.uploads) != 0 || len(f.objects) != 0 {
		t.Fatalf("l'envoi multipart doit être annulé : %v %v", f.uploads, f.keys())
	}
	if last := f.log[len(f.log)-1]; !strings.HasPrefix(last, "DELETE /sauvegardes/k?uploadId=") {
		t.Fatalf("AbortMultipartUpload attendu, dernière requête : %s", last)
	}
}

func TestSignatureChecked(t *testing.T) {
	f, srv := newFakeS3(t)
	cl := newClient(t, f, srv.URL)
	cl.cred.secret = "mauvais"
	err := cl.put(context.Background(), "k", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") || !strings.Contains(err.Error(), "clé secrète incorrecte") {
		t.Fatalf("signature refusée attendue : %v", err)
	}
	cl = newClient(t, f, srv.URL)
	cl.cred.region = "us-east-1"
	if err := cl.put(context.Background(), "k", []byte("x")); err == nil || !strings.Contains(err.Error(), "région incorrecte") {
		t.Fatalf("mauvaise région : %v", err)
	}
	cl = newClient(t, f, srv.URL)
	cl.bucket = "autre"
	if err := cl.put(context.Background(), "k", []byte("x")); err == nil || !strings.Contains(err.Error(), "bucket introuvable") {
		t.Fatalf("bucket absent : %v", err)
	}
}

func TestListPagesAndPrune(t *testing.T) {
	f, srv := newFakeS3(t)
	f.pageSize = 2
	cl := newClient(t, f, srv.URL)
	base := time.Date(2026, 10, 20, 3, 30, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		day := base.AddDate(0, 0, i)
		f.put(fmt.Sprintf("vigie/sonarr/%s.tar.gz", day.Format(stampLayout)), []byte{byte(i)}, day)
	}
	f.put("vigie/sonarr/notes.txt", []byte("à garder"), base)
	f.put("vigie/sonarr-old/2026-01-01_0000.tar.gz", []byte("autre élément"), base)

	objs, err := cl.list(context.Background(), "vigie/sonarr/", 100)
	if err != nil || len(objs) != 6 {
		t.Fatalf("liste paginée : %v %+v", err, objs)
	}
	s := &Service{log: discard()}
	if err := s.prune(context.Background(), cl, "vigie/sonarr", 2); err != nil {
		t.Fatal(err)
	}
	want := []string{"vigie/sonarr-old/2026-01-01_0000.tar.gz", "vigie/sonarr/2026-10-23_0330.tar.gz", "vigie/sonarr/2026-10-24_0330.tar.gz", "vigie/sonarr/notes.txt"}
	if got := f.keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rétention : %v, attendu %v", got, want)
	}
}
