package backup

// Signature AWS Version 4 (en-tête Authorization), implémentée sans dépendance : c'est ce
// qu'attendent tous les stockages compatibles S3 (Oracle Object Storage, Backblaze B2,
// Cloudflare R2, MinIO).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigAlgorithm = "AWS4-HMAC-SHA256"
	amzDate      = "20060102T150405Z"
	// emptySHA256 est l'empreinte d'un corps vide.
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// credentials identifie le signataire et la portée de la signature.
type credentials struct {
	access, secret, region, service string
}

// canonical décrit une requête telle qu'elle est signée.
type canonical struct {
	method  string
	uri     string            // chemin déjà encodé (uriEncode, « / » conservés)
	query   string            // canonicalQuery
	headers map[string]string // noms en minuscules → valeur ; host et x-amz-date compris
	payload string            // SHA-256 hexadécimal du corps
}

func (c canonical) signedHeaders() string {
	names := make([]string, 0, len(c.headers))
	for k := range c.headers {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ";")
}

func (c canonical) request() string {
	var b strings.Builder
	b.WriteString(c.method + "\n" + c.uri + "\n" + c.query + "\n")
	for _, k := range strings.Split(c.signedHeaders(), ";") {
		// Espaces de tête et de fin retirés, suites d'espaces réduites à une seule.
		b.WriteString(k + ":" + strings.Join(strings.Fields(c.headers[k]), " ") + "\n")
	}
	b.WriteString("\n" + c.signedHeaders() + "\n" + c.payload)
	return b.String()
}

// authorization renvoie la valeur de l'en-tête Authorization pour la requête signée à l'instant t.
func authorization(c canonical, cred credentials, t time.Time) string {
	t = t.UTC()
	day := t.Format("20060102")
	scope := day + "/" + cred.region + "/" + cred.service + "/aws4_request"
	toSign := sigAlgorithm + "\n" + t.Format(amzDate) + "\n" + scope + "\n" + hexSHA256([]byte(c.request()))
	key := hmacSHA256([]byte("AWS4"+cred.secret), day)
	for _, part := range []string{cred.region, cred.service, "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	return sigAlgorithm + " Credential=" + cred.access + "/" + scope + ", SignedHeaders=" + c.signedHeaders() + ", Signature=" + sig
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// uriEncode encode tout sauf les caractères non réservés (A-Z a-z 0-9 - _ . ~), en majuscules,
// et garde les « / » si keepSlash (chemin d'un objet S3, qui n'est jamais encodé deux fois).
func uriEncode(s string, keepSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

// canonicalQuery trie les paramètres par nom puis valeur encodés ; un paramètre sans valeur
// s'écrit « nom= ». Le résultat sert aussi tel quel de chaîne de requête envoyée.
func canonicalQuery(q url.Values) string {
	type pair struct{ k, v string }
	pairs := make([]pair, 0, len(q))
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, pair{uriEncode(k, false), uriEncode(v, false)})
		}
	}
	// Tri par nom puis par valeur (et non sur « nom=valeur » : « Param » précède « Param-3 »).
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.k + "=" + p.v
	}
	return strings.Join(out, "&")
}
