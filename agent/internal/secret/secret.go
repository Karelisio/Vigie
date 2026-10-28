// Package secret protège les clés et jetons confiés à l'agent (canaux d'alerte, comptes debrid) :
// ils ne sont renvoyés que masqués et ne doivent jamais apparaître dans un message d'erreur.
package secret

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
)

// Prefix commence toute valeur masquée.
const Prefix = "••••"

// Mask masque une valeur : seuls les 4 derniers caractères restent visibles (aucun pour une valeur courte).
func Mask(v string) string {
	switch r := []rune(v); {
	case len(r) == 0:
		return ""
	case len(r) < 12:
		return Prefix
	default:
		return Prefix + string(r[len(r)-4:])
	}
}

// Merge renvoie la valeur reçue, ou la valeur actuelle si l'appli a renvoyé la valeur masquée ou rien.
func Merge(in, current string) string {
	in = strings.TrimSpace(in)
	if in == "" || strings.HasPrefix(in, Prefix) {
		return current
	}
	return in
}

// Describe décrit une erreur réseau en français, sans l'adresse de la requête
// (qui peut contenir une clé : jeton Telegram, webhook Discord, apikey).
func Describe(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "délai dépassé"
	case errors.Is(err, context.Canceled):
		return "requête annulée"
	case errors.As(err, &certErr):
		return "certificat HTTPS invalide"
	case errors.As(err, &dnsErr):
		return "adresse introuvable (DNS)"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "connexion refusée ou injoignable"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return "délai dépassé"
		}
		return "connexion impossible : " + ue.Err.Error()
	}
	return err.Error()
}
