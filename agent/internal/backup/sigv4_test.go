package backup

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vecteurs de la suite de tests officielle AWS Signature Version 4
// (github.com/awslabs/aws-c-auth, tests/aws-signing-test-suite/v4).
func TestSigV4TestSuite(t *testing.T) {
	cred := credentials{access: "AKIDEXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", region: "us-east-1", service: "service"}
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	for _, c := range []struct {
		name, method, path, query string
		headers                   map[string]string
		payload                   string
		want                      string
	}{
		{"get-vanilla", "GET", "/", "", nil, emptySHA256,
			"5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-vanilla-query-order-key-case", "GET", "/", "Param2=value2&Param1=value1", nil, emptySHA256,
			"b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"},
		{"get-vanilla-query-order-encoded", "GET", "/", "Param-3=Value3&Param=Value2&%E1%88%B4=Value1", nil, emptySHA256,
			"371d3713e185cc334048618a97f809c9ffe339c62934c032af5a0e595648fcac"},
		{"get-unreserved", "GET", "/-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", "", nil, emptySHA256,
			"07ef7494c76fa4850883e2b006601f940f8a34d404d0cfa977f52a65bbf5f24f"},
		{"get-vanilla-query-unreserved", "GET", "/",
			"-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz=-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
			nil, emptySHA256, "9c3e54bfcdf0b19771a7f523ee5669cdf59bc7cc0884027167c21bb143a40197"},
		{"get-header-value-trim", "GET", "/", "", map[string]string{"my-header1": " value1", "my-header2": ` "a   b   c"`}, emptySHA256,
			"acc3ed3afb60bb290fc8d2dd0098b9911fcaa05412b367055dee359757a9c736"},
		{"post-vanilla-empty-query-value", "POST", "/", "Param1=value1", nil, emptySHA256,
			"28038455d6de14eafc1f9222cf5aa6f1a96197d7deb8263271d420d138af7f11"},
		{"post-x-www-form-urlencoded", "POST", "/", "", map[string]string{
			"content-length": "13", "content-type": "application/x-www-form-urlencoded",
			"x-amz-content-sha256": hexSHA256([]byte("Param1=value1")),
		}, hexSHA256([]byte("Param1=value1")), "d3875051da38690788ef43de4db0d8f280229d82040bfac253562e56c3f20e0b"},
	} {
		q, err := url.ParseQuery(c.query)
		if err != nil {
			t.Fatal(err)
		}
		h := map[string]string{"host": "example.amazonaws.com", "x-amz-date": at.Format(amzDate)}
		for k, v := range c.headers {
			h[k] = v
		}
		got := authorization(canonical{c.method, uriEncode(c.path, true), canonicalQuery(q), h, c.payload}, cred, at)
		if !strings.HasSuffix(got, "Signature="+c.want) {
			t.Errorf("%s : %s", c.name, got)
		}
	}
}

func TestCanonicalRequest(t *testing.T) {
	q, _ := url.ParseQuery("Param-3=Value3&Param=Value2&%E1%88%B4=Value1")
	c := canonical{"GET", "/", canonicalQuery(q), map[string]string{"host": "example.amazonaws.com", "x-amz-date": "20150830T123600Z"}, emptySHA256}
	want := "GET\n/\n%E1%88%B4=Value1&Param=Value2&Param-3=Value3\nhost:example.amazonaws.com\nx-amz-date:20150830T123600Z\n\nhost;x-amz-date\n" + emptySHA256
	if got := c.request(); got != want {
		t.Errorf("requête canonique :\n%s\nattendu :\n%s", got, want)
	}
}

// Exemples de la documentation S3 (« Signature Calculations for the Authorization Header »).
func TestSigV4S3Examples(t *testing.T) {
	cred := credentials{access: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", region: "us-east-1", service: "s3"}
	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	host := "examplebucket.s3.amazonaws.com"
	for _, c := range []struct {
		name, method, key, query string
		headers                  map[string]string
		payload                  string
		want                     string
	}{
		{"GET Object", "GET", "/test.txt", "", map[string]string{"range": "bytes=0-9"}, emptySHA256,
			"f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"},
		{"PUT Object", "PUT", "/test$file.text", "", map[string]string{
			"date": "Fri, 24 May 2013 00:00:00 GMT", "x-amz-storage-class": "REDUCED_REDUNDANCY",
		}, hexSHA256([]byte("Welcome to Amazon S3.")), "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"},
		{"GET Bucket Lifecycle", "GET", "/", "lifecycle", nil, emptySHA256,
			"fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"},
		{"GET Bucket (List Objects)", "GET", "/", "max-keys=2&prefix=J", nil, emptySHA256,
			"34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"},
	} {
		q, _ := url.ParseQuery(c.query)
		h := map[string]string{"host": host, "x-amz-date": at.Format(amzDate), "x-amz-content-sha256": c.payload}
		for k, v := range c.headers {
			h[k] = v
		}
		got := authorization(canonical{c.method, uriEncode(c.key, true), canonicalQuery(q), h, c.payload}, cred, at)
		if !strings.HasSuffix(got, "Signature="+c.want) {
			t.Errorf("%s : %s", c.name, got)
		}
	}
}

func TestURIEncode(t *testing.T) {
	if got := uriEncode("dossier/fichier $ é~.tar.gz", true); got != "dossier/fichier%20%24%20%C3%A9~.tar.gz" {
		t.Errorf("chemin : %s", got)
	}
	if got := uriEncode("a/b", false); got != "a%2Fb" {
		t.Errorf("valeur : %s", got)
	}
}
