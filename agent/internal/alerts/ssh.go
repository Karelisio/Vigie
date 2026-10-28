package alerts

import (
	"fmt"
	"time"

	"github.com/karelisio/vigie/agent/internal/sshwatch"
)

var sshMethods = map[string]string{
	"publickey":            "clé SSH",
	"password":             "mot de passe",
	"keyboard-interactive": "mot de passe (clavier interactif)",
	"hostbased":            "authentification par hôte",
	"gssapi-with-mic":      "Kerberos",
}

// NotifySSHLogin signale une connexion SSH réussie (réglage ssh_login_alert) : warning depuis une adresse
// jamais vue, info sinon. Les simples tentatives ne sont jamais signalées (bruit permanent).
func (e *Engine) NotifySSHLogin(l sshwatch.Login) {
	if !e.set.Get().SSHLoginAlert {
		return
	}
	where := l.IP
	if l.CountryName != "" {
		where += " (" + l.CountryName + ")"
	}
	n := Notification{Kind: "ssh", Severity: "info", Target: l.User, Title: "Connexion SSH : " + l.User + " depuis " + where}
	method := sshMethods[l.Method]
	if method == "" {
		method = l.Method
	}
	n.Body = "Le " + time.UnixMilli(l.TS).Format("02/01 à 15:04")
	if method != "" {
		n.Body += ", par " + method
	}
	n.Body += "."
	if l.NewIP {
		n.Severity = "warning"
		n.Body += " Première connexion depuis cette adresse."
	}
	if l.Failures > 0 {
		n.Body += fmt.Sprintf(" Elle a échoué %d fois ces 7 derniers jours.", l.Failures)
	}
	if l.Others > 0 {
		n.Body += fmt.Sprintf(" Et %d autre(s) connexion(s) au même moment.", l.Others)
	}
	e.dispatch(n, false, true)
}
