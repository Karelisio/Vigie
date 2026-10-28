package backup

// Client S3 minimal (adresse « path-style » : https://hôte/bucket/clé) : envoi simple ou multipart,
// liste (ListObjectsV2) et suppression. Les corps sont toujours signés (x-amz-content-sha256).

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// partSize est la taille d'une partie d'envoi multipart : la seule mémoire tampon d'une sauvegarde.
	partSize = 16 << 20
	// maxParts est la limite S3 (10 000 parties, soit 156 Gio par archive avec des parties de 16 Mio).
	maxParts = 10000
	attempts = 3 // essais par requête en cas d'erreur réseau ou de stockage indisponible
)

type s3Client struct {
	endpoint *url.URL
	bucket   string
	cred     credentials
	http     *http.Client
	retryGap time.Duration // pause avant un nouvel essai (raccourcie dans les tests)
}

// s3HTTP n'a pas de délai global (une partie peut prendre du temps sur une connexion lente) mais
// abandonne un stockage qui ne répond plus.
var s3HTTP = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 2 * time.Minute,
	IdleConnTimeout:       time.Minute,
	MaxIdleConnsPerHost:   2,
}}

func newS3(c Config, s secrets) (*s3Client, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, err
	}
	return &s3Client{
		endpoint: u, bucket: c.Bucket, http: s3HTTP, retryGap: 2 * time.Second,
		cred: credentials{access: s.AccessKey, secret: s.SecretKey, region: c.Region, service: "s3"},
	}, nil
}

// s3Error est une réponse d'erreur du stockage.
type s3Error struct {
	Status  int
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// s3Hints explique les erreurs les plus courantes d'une configuration.
var s3Hints = map[string]string{
	"InvalidAccessKeyId":           "clé d'accès inconnue",
	"SignatureDoesNotMatch":        "clé secrète incorrecte",
	"AuthorizationHeaderMalformed": "région incorrecte",
	"NoSuchBucket":                 "bucket introuvable",
	"AccessDenied":                 "accès refusé : vérifie les droits de la clé sur ce bucket",
	"RequestTimeTooSkewed":         "l'horloge du serveur est décalée",
}

func (e *s3Error) Error() string {
	msg := fmt.Sprintf("stockage : %d %s", e.Status, e.Code)
	if h := s3Hints[e.Code]; h != "" {
		msg += " (" + h + ")"
	} else if e.Message != "" {
		msg += " (" + e.Message + ")"
	}
	return msg
}

// retryable : erreur réseau, stockage surchargé ou indisponible.
func retryable(err error) bool {
	var se *s3Error
	if errors.As(err, &se) {
		return se.Status == http.StatusTooManyRequests || se.Status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// do envoie une requête signée (avec nouveaux essais) et décode la réponse XML dans out si besoin.
// key vide : requête sur le bucket.
func (c *s3Client) do(ctx context.Context, method, key string, q url.Values, body []byte, out any) (http.Header, error) {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryGap * time.Duration(i)):
			}
		}
		var h http.Header
		h, err = c.once(ctx, method, key, q, body, out)
		if err == nil || !retryable(err) {
			return h, err
		}
	}
	return nil, err
}

// request prépare une requête signée. key vide : requête sur le bucket.
func (c *s3Client) request(ctx context.Context, method, key string, q url.Values, body []byte) (*http.Request, error) {
	path := "/" + c.bucket
	if key != "" {
		path += "/" + key
	}
	u := *c.endpoint
	u.Path, u.RawPath = path, uriEncode(path, true)
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	payload := hexSHA256(body)
	req.Header.Set("X-Amz-Date", now.Format(amzDate))
	req.Header.Set("X-Amz-Content-Sha256", payload)
	if method == http.MethodPost && body != nil {
		req.Header.Set("Content-Type", "application/xml")
	}
	sig := canonical{
		method: method, uri: u.EscapedPath(), query: u.RawQuery, payload: payload,
		headers: map[string]string{"host": req.URL.Host, "x-amz-date": now.Format(amzDate), "x-amz-content-sha256": payload},
	}
	req.Header.Set("Authorization", authorization(sig, c.cred, now))
	return req, nil
}

// errorOf décode la réponse d'erreur du stockage.
func errorOf(status int, raw []byte) *s3Error {
	e := &s3Error{Status: status}
	_ = xml.Unmarshal(raw, e)
	if e.Code == "" {
		e.Code = http.StatusText(status)
	}
	return e
}

func (c *s3Client) once(ctx context.Context, method, key string, q url.Values, body []byte, out any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := c.request(ctx, method, key, q, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Les réponses XML attendues (liste, multipart) restent petites ; au-delà, c'est une erreur.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, errorOf(resp.StatusCode, raw)
	}
	// CompleteMultipartUpload peut répondre 200 avec une erreur dans le corps.
	if bytes.Contains(raw[:min(len(raw), 256)], []byte("<Error>")) {
		e := &s3Error{Status: resp.StatusCode}
		_ = xml.Unmarshal(raw, e)
		return nil, e
	}
	if out != nil {
		if err := xml.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("stockage : réponse illisible (%w)", err)
		}
	}
	return resp.Header, nil
}

func (c *s3Client) put(ctx context.Context, key string, body []byte) error {
	_, err := c.do(ctx, http.MethodPut, key, nil, body, nil)
	return err
}

func (c *s3Client) delete(ctx context.Context, key string) error {
	_, err := c.do(ctx, http.MethodDelete, key, nil, nil, nil)
	return err
}

// Upload envoie le flux r sous la clé key et renvoie sa taille : en une requête s'il tient dans
// buf, sinon en envoi multipart par parties de len(buf) octets. buf est la seule mémoire utilisée.
func (c *s3Client) Upload(ctx context.Context, key string, r io.Reader, buf []byte, sent func(int64)) (int64, error) {
	n, err := io.ReadFull(r, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if err := c.put(ctx, key, buf[:n]); err != nil {
			return 0, err
		}
		sent(int64(n))
		return int64(n), nil
	}
	if err != nil {
		return 0, err
	}

	var init struct {
		UploadID string `xml:"UploadId"`
	}
	if _, err := c.do(ctx, http.MethodPost, key, url.Values{"uploads": {""}}, nil, &init); err != nil {
		return 0, err
	}
	if init.UploadID == "" {
		return 0, errors.New("stockage : identifiant d'envoi multipart absent")
	}
	abort := func(cause error) (int64, error) {
		actx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = c.do(actx, http.MethodDelete, key, url.Values{"uploadId": {init.UploadID}}, nil, nil)
		return 0, cause
	}

	type part struct {
		Number int    `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
	}
	var parts []part
	var total int64
	for {
		num := len(parts) + 1
		if num > maxParts {
			return abort(fmt.Errorf("archive trop volumineuse (plus de %d parties)", maxParts))
		}
		q := url.Values{"partNumber": {strconv.Itoa(num)}, "uploadId": {init.UploadID}}
		h, err := c.do(ctx, http.MethodPut, key, q, buf[:n], nil)
		if err != nil {
			return abort(fmt.Errorf("partie %d : %w", num, err))
		}
		parts = append(parts, part{num, h.Get("ETag")})
		total += int64(n)
		sent(total)
		// La dernière partie peut être plus courte (ErrUnexpectedEOF) ; EOF : plus rien à envoyer.
		n, err = io.ReadFull(r, buf)
		if err == io.EOF {
			break
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return abort(err)
		}
	}

	body, _ := xml.Marshal(struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{Parts: parts})
	if _, err := c.do(ctx, http.MethodPost, key, url.Values{"uploadId": {init.UploadID}}, body, nil); err != nil {
		return abort(err)
	}
	return total, nil
}

// open lit l'objet key en flux et renvoie son corps (à fermer) et sa taille. Les nouveaux essais
// ne portent que sur l'ouverture : une coupure pendant la lecture est une erreur pour l'appelant.
func (c *s3Client) open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(c.retryGap * time.Duration(i)):
			}
		}
		var body io.ReadCloser
		var size int64
		body, size, err = c.openOnce(ctx, key)
		if err == nil || !retryable(err) {
			return body, size, err
		}
	}
	return nil, 0, err
}

func (c *s3Client) openOnce(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	req, err := c.request(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, 0, errorOf(resp.StatusCode, raw)
	}
	if resp.ContentLength < 0 {
		resp.Body.Close()
		return nil, 0, errors.New("stockage : taille de la copie inconnue")
	}
	return resp.Body, resp.ContentLength, nil
}

// Object est une sauvegarde présente dans le stockage.
type Object struct {
	Key    string `json:"key"`
	Item   string `json:"item"` // dossier de l'élément
	Size   int64  `json:"size"`
	TS     int64  `json:"ts"`     // dernière modification (ms)
	Safety bool   `json:"safety"` // copie de sécurité faite avant la dernière restauration
}

// list renvoie les objets dont la clé commence par prefix (au plus limit).
func (c *s3Client) list(ctx context.Context, prefix string, limit int) ([]Object, error) {
	var out []Object
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		var page struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		if _, err := c.do(ctx, http.MethodGet, "", q, nil, &page); err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			out = append(out, Object{Key: o.Key, Size: o.Size, TS: o.LastModified.UnixMilli()})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" || len(out) >= limit {
			return out[:min(len(out), limit)], nil
		}
		token = page.NextContinuationToken
	}
}
