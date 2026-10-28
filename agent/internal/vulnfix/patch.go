package vulnfix

// Fabrication d'une image corrigée, sans BuildKit ni Dockerfile :
//  1. un conteneur temporaire est créé depuis l'image (en root, sans les volumes ni les réglages du
//     conteneur d'origine), sa base de paquets est lue ;
//  2. il exécute la mise à niveau des seuls paquets concernés (apt-get --only-upgrade, apk upgrade) ;
//  3. la base de paquets est relue : si rien n'a changé, rien n'est créé ;
//  4. sinon `POST /commit` crée l'image dérivée avec exactement la configuration de l'image de départ
//     (Cmd, Entrypoint, User, WorkingDir, Env, ports, volumes, labels, healthcheck, signal d'arrêt…)
//     plus les labels Vigie. Docker complète les champs vides de cette configuration avec ceux du
//     conteneur : sa commande est donc posée là où l'image n'en a pas besoin (Entrypoint si l'image en
//     a un, sinon Cmd), et rien d'autre ne diffère de l'image (ni variables, ni labels) ;
//  5. la configuration de l'image obtenue est comparée champ par champ à celle de départ : au moindre
//     écart, l'image est supprimée et la correction abandonnée.
// Le conteneur temporaire est toujours supprimé (avec ses volumes anonymes).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

// imageFields : configuration d'une image recopiée dans l'image corrigée, puis vérifiée.
var imageFields = []string{
	"User", "ExposedPorts", "Env", "Cmd", "Healthcheck", "ArgsEscaped", "Volumes", "WorkingDir",
	"Entrypoint", "OnBuild", "Labels", "StopSignal", "Shell",
}

var errNoCommand = errors.New("image sans commande par défaut (ni ENTRYPOINT ni CMD) : impossible d'en dériver une image fidèle")

// Ressources du conteneur temporaire.
const (
	tempMemory = 1 << 30
	tempPids   = 512
)

type builder struct {
	dk        *docker.Client
	log       *slog.Logger
	network   string        // réseau du conteneur temporaire (bridge par défaut)
	poll      time.Duration // attente entre deux lectures de l'état
	timeout   time.Duration // durée maximale de la mise à niveau
	transient func(name string, on bool)
	note      func(name, action string)
	now       func() time.Time
}

type buildSpec struct {
	Base     string         // image de départ (sha256:…)
	BaseCfg  map[string]any // sa configuration
	Manager  string         // apt, apk
	Args     []string       // paquets binaires à mettre à niveau (validés)
	Patched  images.Patched // labels à poser ; Packages : correction précédente à cumuler
	Cumulate bool           // Base est déjà une image corrigée : cumuler Patched.Packages
	// Current : paquets de l'image en service quand elle diffère de Base (image déjà corrigée,
	// refaite depuis l'origine) : si le résultat est identique, rien n'a changé.
	Current []vulns.Binary
}

type buildResult struct {
	Image   string                 // image corrigée, « » si rien n'a changé
	Before  vulns.Inventory        // paquets de l'image de départ
	After   vulns.Inventory        // paquets après la mise à niveau
	Changes []images.PackageChange // paquets changés (depuis l'origine)
	Note    string                 // quand rien n'a changé : explication (dépôts injoignables…)
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	case string:
		return t == ""
	case bool:
		return !t
	}
	return false
}

// tempSpec prépare le conteneur temporaire : la commande remplace l'Entrypoint si l'image en a un,
// sinon la Cmd (voir la note en tête du fichier).
func tempSpec(base string, cfg map[string]any, script, network string) (map[string]any, error) {
	cmd := []string{"/bin/sh", "-c", script}
	spec := map[string]any{"Image": base}
	switch {
	case !isEmpty(cfg["Entrypoint"]):
		spec["Entrypoint"] = cmd
	case !isEmpty(cfg["Cmd"]):
		spec["Cmd"] = cmd
	default:
		return nil, errNoCommand
	}
	if u, _ := cfg["User"].(string); u != "" && u != "0" && u != "root" && !strings.HasPrefix(u, "0:") && !strings.HasPrefix(u, "root:") {
		spec["User"] = "0" // la configuration fournie au commit garde l'utilisateur de l'image
	}
	if !isEmpty(cfg["Healthcheck"]) {
		// Pas de healthcheck pendant la mise à niveau. Sans effet sur l'image : ses champs non vides l'emportent.
		spec["Healthcheck"] = map[string]any{"Test": []string{"NONE"}}
	}
	if network == "" {
		network = "bridge"
	}
	spec["HostConfig"] = map[string]any{
		"NetworkMode":   network,
		"RestartPolicy": map[string]any{"Name": "no"},
		"Memory":        tempMemory,
		"NanoCpus":      int64(1e9),
		"PidsLimit":     tempPids,
		"CapDrop":       []string{"NET_RAW"},
		"SecurityOpt":   []string{"no-new-privileges:true"},
		"LogConfig":     map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m"}},
	}
	return spec, nil
}

// commitConfig : configuration de l'image de départ (champs d'image seulement) et labels Vigie.
func commitConfig(base map[string]any, labels map[string]string) map[string]any {
	out := map[string]any{}
	for _, k := range imageFields {
		if v, ok := base[k]; ok && v != nil {
			out[k] = v
		}
	}
	merged := map[string]any{}
	if l, ok := base["Labels"].(map[string]any); ok {
		for k, v := range l {
			merged[k] = v
		}
	}
	for k, v := range labels {
		merged[k] = v
	}
	out["Labels"] = merged
	return out
}

// sameConfig compare la configuration de l'image corrigée à celle attendue ; renvoie le premier champ
// différent (« » si fidèle). Vide, absent et null sont équivalents.
func sameConfig(want, got map[string]any) string {
	for _, k := range imageFields {
		a, b := want[k], got[k]
		if isEmpty(a) && isEmpty(b) {
			continue
		}
		if !reflect.DeepEqual(a, b) {
			return k
		}
	}
	return ""
}

// build fabrique l'image corrigée. progress reçoit l'avancement de 0 à 1.
func (b *builder) build(ctx context.Context, spec buildSpec, progress func(p float64, msg string)) (res buildResult, err error) {
	script := upgradeScript(spec.Manager, spec.Args)
	body, err := tempSpec(spec.Base, spec.BaseCfg, script, b.network)
	if err != nil {
		return res, err
	}
	name := fmt.Sprintf("%s%d", vulns.TempPatchPrefix, b.now().UnixNano())
	b.transient(name, true)
	b.note(name, "vulnfix")
	progress(0.02, "Création du conteneur temporaire…")
	id, err := b.dk.Create(ctx, name, body)
	if err != nil {
		b.transient(name, false)
		return res, fmt.Errorf("conteneur temporaire : %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if rerr := b.dk.RemoveWithVolumes(rctx, id); rerr != nil && !errors.Is(rerr, docker.ErrNotFound) {
			b.log.Warn("correction : conteneur temporaire non supprimé", "conteneur", name, "erreur", rerr)
		}
		b.transient(name, false)
	}()

	res.Before = vulns.ReadInventory(ctx, b.dk, id, nil, b.now())
	if res.Before.Error != "" {
		return res, fmt.Errorf("lecture des paquets : %s", res.Before.Error)
	}
	progress(0.08, fmt.Sprintf("Mise à niveau de %s…", plural(len(spec.Args), "paquet", "paquets")))
	if err := b.dk.Start(ctx, id); err != nil {
		return res, fmt.Errorf("démarrage du conteneur temporaire : %w", err)
	}
	code, err := b.wait(ctx, id, len(spec.Args), progress)
	b.note(name, "vulnfix") // pas d'alerte « arrêté » pour le conteneur temporaire
	if err != nil {
		return res, err
	}
	lctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	logs := b.dk.LogTail(lctx, id, 40)
	cancel()
	if code != 0 {
		if unreachable(logs) {
			return res, fmt.Errorf("dépôts de paquets injoignables depuis le conteneur temporaire, rien n'a été modifié (code %d)%s", code, logExcerpt(logs, true))
		}
		return res, fmt.Errorf("la mise à niveau des paquets a échoué, rien n'a été modifié (code %d)%s", code, logExcerpt(logs, true))
	}

	progress(0.62, "Vérification des paquets mis à niveau…")
	res.After = vulns.ReadInventory(ctx, b.dk, id, nil, b.now())
	if res.After.Error != "" {
		return res, fmt.Errorf("lecture des paquets après la mise à niveau : %s", res.After.Error)
	}
	changes := diffBins(res.Before.Binaries(), res.After.Binaries())
	unchanged := len(changes) == 0
	if spec.Current != nil {
		unchanged = len(diffBins(spec.Current, res.After.Binaries())) == 0
	}
	if unchanged {
		res.Note = repoNote(logs)
		return res, nil
	}
	res.Changes = changes
	if spec.Cumulate {
		res.Changes = mergeChanges(spec.Patched.Packages, changes)
	}

	progress(0.7, "Création de l'image corrigée…")
	patched := spec.Patched
	patched.Packages = res.Changes
	patched.At = b.now().UnixMilli()
	want := commitConfig(spec.BaseCfg, patched.Labels())
	comment := "Vigie : " + describeChanges(changes, 8)
	image, err := b.dk.Commit(ctx, id, truncate(comment, 1000), "Vigie", want)
	if err != nil {
		return res, fmt.Errorf("création de l'image corrigée : %w", err)
	}
	progress(0.85, "Comparaison avec l'image d'origine…")
	got, err := b.dk.ImageDetails(ctx, image)
	if err == nil {
		if field := sameConfig(want, got.Config); field != "" {
			err = fmt.Errorf("configuration de l'image corrigée différente de l'origine (%s) : correction abandonnée", field)
		}
	}
	if err != nil {
		if rerr := b.dk.RemoveImage(context.WithoutCancel(ctx), image); rerr != nil {
			b.log.Warn("correction : image non fidèle non supprimée", "image", image, "erreur", rerr)
		}
		return res, err
	}
	res.Image = image
	return res, nil
}

// wait attend la fin du conteneur temporaire et renvoie son code de sortie.
func (b *builder) wait(ctx context.Context, id string, n int, progress func(float64, string)) (int, error) {
	start := b.now()
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	for {
		in, err := b.dk.InspectFull(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return 0, fmt.Errorf("mise à niveau trop longue (plus de %s) : abandon", b.timeout)
			}
			return 0, err
		}
		if !in.State.Running && in.State.Status != "created" {
			return in.State.ExitCode, nil
		}
		elapsed := b.now().Sub(start)
		// Avancement estimé : la durée réelle dépend des dépôts (40 % de la barre en ~2 min).
		p := 0.1 + 0.5*(1-1/(1+elapsed.Seconds()/90))
		progress(p, fmt.Sprintf("Mise à niveau de %s (%s)…", plural(n, "paquet", "paquets"), elapsed.Round(time.Second)))
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("mise à niveau trop longue (plus de %s) : abandon", b.timeout)
		case <-time.After(b.poll):
		}
	}
}

// logExcerpt garde les lignes utiles du journal du conteneur temporaire (erreurs et avertissements).
func logExcerpt(lines []string, fallback bool) string {
	var keep []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "E:") || strings.HasPrefix(t, "W:") || strings.HasPrefix(t, "ERROR") ||
			strings.HasPrefix(t, "WARNING") || strings.HasPrefix(t, "Err:") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 && fallback {
		for i := len(lines) - 1; i >= 0 && len(keep) < 2; i-- {
			if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "vigie:") {
				keep = append([]string{t}, keep...)
			}
		}
	}
	if len(keep) > 3 {
		keep = keep[len(keep)-3:]
	}
	if len(keep) == 0 {
		return ""
	}
	return " : " + truncate(strings.Join(keep, " | "), 400)
}

// unreachable : le journal montre des dépôts de paquets injoignables.
func unreachable(lines []string) bool {
	for _, l := range lines {
		for _, s := range []string{"Failed to fetch", "Could not resolve", "Could not connect", "Temporary failure",
			"temporary error", "network error", "DNS lookup error", "Connection refused", "timed out"} {
			if strings.Contains(l, s) {
				return true
			}
		}
	}
	return false
}

// repoNote explique pourquoi rien n'a changé.
func repoNote(lines []string) string {
	if unreachable(lines) {
		return "les dépôts de paquets sont injoignables depuis le conteneur temporaire" + logExcerpt(lines, false)
	}
	return "les dépôts de l'image ne proposent pas encore de version plus récente de ces paquets"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
