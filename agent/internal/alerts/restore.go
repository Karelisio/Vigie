package alerts

// NotifyRestore signale la fin de la restauration d'un élément (item : son nom). Un échec est une
// alerte critique, envoyée même appli ouverte et close par la prochaine restauration réussie du même
// élément ; une réussite est notifiée comme les autres opérations (appli fermée).
func (e *Engine) NotifyRestore(item string, ok bool, summary, failure string) {
	key := "restore:" + item
	n := Notification{Kind: "restore", Target: item}
	if !ok {
		n.Severity, n.Title, n.Body = "critical", "Échec de la restauration de "+item, failure
		e.fire(key, n)
		return
	}
	e.mu.Lock()
	active := e.active[key]
	e.mu.Unlock()
	if active {
		n.Severity, n.Title, n.Body = "info", "Restauration de "+item+" réussie", summary
		e.resolve(key, n)
		return
	}
	e.NotifyJob("Restauration de "+item+" terminée", summary, true)
}
