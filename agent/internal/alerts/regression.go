package alerts

// NotifyRegression signale un conteneur qui consomme nettement plus depuis sa mise à jour.
func (e *Engine) NotifyRegression(name, title, body string) {
	if e.cfg.Ignore[name] || e.set.Get().IsMuted(name) {
		return
	}
	e.dispatch(Notification{Kind: "regression", Severity: "warning", Target: name, Title: title, Body: body}, false, true)
}
