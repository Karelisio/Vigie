package alerts

// backupKey : une seule alerte de sauvegarde à la fois, close par la prochaine réussite.
const backupKey = "backup"

// NotifyBackup signale la fin d'une sauvegarde. Un échec est une alerte critique, envoyée même
// appli ouverte ; une réussite n'est notifiée que si l'appli est fermée (comme les autres
// opérations), ou comme retour à la normale après un échec.
func (e *Engine) NotifyBackup(ok bool, summary, failure string) {
	n := Notification{Kind: "backup", Target: "sauvegarde"}
	if !ok {
		n.Severity, n.Title, n.Body = "critical", "Échec de la sauvegarde", failure
		e.fire(backupKey, n)
		return
	}
	e.mu.Lock()
	active := e.active[backupKey]
	e.mu.Unlock()
	if active {
		n.Severity, n.Title, n.Body = "info", "Sauvegarde de nouveau réussie", summary
		e.resolve(backupKey, n)
		return
	}
	e.NotifyJob("Sauvegarde terminée", summary, true)
}
