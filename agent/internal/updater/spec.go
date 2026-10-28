package updater

import (
	"reflect"
	"sort"
	"strings"

	"github.com/karelisio/vigie/agent/internal/docker"
)

// Construction de la requête de création d'un conteneur identique à l'ancien, avec la nouvelle image.
// Les valeurs héritées de l'ancienne image (Env, Cmd, ports exposés…) sont retirées pour que celles
// de la nouvelle image s'appliquent, comme le ferait `docker compose up` après un pull.

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopy(x)
		}
		return out
	default:
		return v
	}
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return deepCopy(m).(map[string]any)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
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
	}
	return false
}

func same(a, b any) bool {
	if isEmpty(a) && isEmpty(b) {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// stripImageDefaults retire de cfg ce qui provient de l'image d'origine.
func stripImageDefaults(cfg, img map[string]any) {
	if img == nil {
		return
	}
	if env, ok := cfg["Env"].([]any); ok {
		inImage := map[string]bool{}
		if ie, ok := img["Env"].([]any); ok {
			for _, e := range ie {
				if s, ok := e.(string); ok {
					inImage[s] = true
				}
			}
		}
		kept := []any{}
		for _, e := range env {
			if s, ok := e.(string); !ok || !inImage[s] {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(cfg, "Env")
		} else {
			cfg["Env"] = kept
		}
	}
	for _, k := range []string{"Labels", "ExposedPorts", "Volumes"} {
		cm, ok := cfg[k].(map[string]any)
		if !ok {
			continue
		}
		im, _ := img[k].(map[string]any)
		for key, v := range cm {
			if iv, found := im[key]; found && (k != "Labels" || reflect.DeepEqual(iv, v)) {
				delete(cm, key)
			}
		}
		if len(cm) == 0 {
			delete(cfg, k)
		}
	}
	entrypointDefault := same(cfg["Entrypoint"], img["Entrypoint"])
	if entrypointDefault {
		delete(cfg, "Entrypoint")
		// Sans point d'entrée personnalisé, une commande identique à celle de l'image vient de l'image.
		if same(cfg["Cmd"], img["Cmd"]) {
			delete(cfg, "Cmd")
		}
	}
	for _, k := range []string{"WorkingDir", "User", "StopSignal", "Healthcheck", "Shell", "OnBuild"} {
		if same(cfg[k], img[k]) {
			delete(cfg, k)
		}
	}
}

// endpointConfig ne garde que la configuration choisie par l'utilisateur (pas l'IP ni la MAC attribuées).
func endpointConfig(ep map[string]any, oldID string) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"IPAMConfig", "Links", "DriverOpts", "GwPriority"} {
		if v, ok := ep[k]; ok && !isEmpty(v) {
			out[k] = deepCopy(v)
		}
	}
	if aliases, ok := ep["Aliases"].([]any); ok {
		var kept []any
		for _, a := range aliases {
			if s, _ := a.(string); s != "" && !(len(s) >= 12 && strings.HasPrefix(oldID, s)) {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			out["Aliases"] = kept
		}
	}
	return out
}

// mountTargets liste les chemins déjà montés explicitement (binds et mounts).
func mountTargets(hc map[string]any) map[string]bool {
	out := map[string]bool{}
	if binds, ok := hc["Binds"].([]any); ok {
		for _, b := range binds {
			if s, ok := b.(string); ok {
				if parts := strings.Split(s, ":"); len(parts) >= 2 {
					out[parts[1]] = true
				}
			}
		}
	}
	if mounts, ok := hc["Mounts"].([]any); ok {
		for _, m := range mounts {
			if mm, ok := m.(map[string]any); ok {
				out[str(mm, "Target")] = true
			}
		}
	}
	return out
}

type createSpec struct {
	Body  map[string]any
	Extra map[string]any // réseaux à connecter après la création (Docker < 25)
}

// buildCreate prépare la création du remplaçant de old.
//   - newImageID met à jour l'étiquette compose qui mémorise l'image (si présente) ;
//   - networkMode remplace « container:<ancien ID> » pour les conteneurs qui partagent le réseau d'un autre ;
//   - multiNet indique si Docker accepte plusieurs réseaux à la création (API ≥ 1.44).
func buildCreate(old *docker.ContainerJSON, oldImage map[string]any, newImageID, networkMode string, multiNet bool) createSpec {
	cfg := copyMap(old.Config)
	stripImageDefaults(cfg, oldImage)
	if h := str(cfg, "Hostname"); h != "" && strings.HasPrefix(old.ID, h) {
		delete(cfg, "Hostname")
	}
	if labels, ok := cfg["Labels"].(map[string]any); ok && newImageID != "" {
		if _, ok := labels["com.docker.compose.image"]; ok {
			labels["com.docker.compose.image"] = newImageID
		}
	}

	hc := copyMap(old.HostConfig)
	if networkMode != "" {
		hc["NetworkMode"] = networkMode
	}
	// Volumes anonymes : on les rattache au nouveau conteneur pour ne pas perdre leurs données.
	covered := mountTargets(hc)
	var extraMounts []any
	for _, m := range old.Mounts {
		if m.Type == "volume" && m.Name != "" && !covered[m.Destination] {
			extraMounts = append(extraMounts, map[string]any{
				"Type": "volume", "Source": m.Name, "Target": m.Destination, "ReadOnly": !m.RW,
			})
		}
	}
	if len(extraMounts) > 0 {
		existing, _ := hc["Mounts"].([]any)
		hc["Mounts"] = append(existing, extraMounts...)
	}

	body := cfg
	body["HostConfig"] = hc
	spec := createSpec{Body: body, Extra: map[string]any{}}

	mode := str(hc, "NetworkMode")
	if strings.HasPrefix(mode, "container:") {
		// Le nom d'hôte et les ports viennent du conteneur qui fournit le réseau : Docker refuse
		// de les recevoir à la création.
		for _, k := range []string{"Hostname", "Domainname", "ExposedPorts", "MacAddress"} {
			delete(cfg, k)
		}
	}
	if strings.HasPrefix(mode, "container:") || mode == "host" || mode == "none" {
		return spec
	}
	endpoints := map[string]any{}
	for name, ep := range old.NetworkSettings.Networks {
		endpoints[name] = endpointConfig(ep, old.ID)
	}
	if len(endpoints) == 0 {
		return spec
	}
	if multiNet {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
		return spec
	}
	primary := mode
	if _, ok := endpoints[primary]; !ok {
		names := make([]string, 0, len(endpoints))
		for n := range endpoints {
			names = append(names, n)
		}
		sort.Strings(names)
		primary = names[0]
	}
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{primary: endpoints[primary]}}
	for n, ep := range endpoints {
		if n != primary {
			spec.Extra[n] = ep
		}
	}
	return spec
}

// buildHelper prépare le conteneur auxiliaire qui remplace l'agent : même image (le code en cours),
// mêmes accès à Docker, mais sans ports publiés, sans alias réseau et supprimé à la fin.
func buildHelper(self *docker.ContainerJSON, extraEnv ...string) map[string]any {
	cfg := copyMap(self.Config)
	cfg["Image"] = self.Image
	delete(cfg, "Hostname")
	delete(cfg, "ExposedPorts")
	env, _ := cfg["Env"].([]any)
	env = append(env, "VIGIE_HELPER_REPLACE="+self.ID)
	for _, e := range extraEnv {
		env = append(env, e)
	}
	cfg["Env"] = env
	labels := map[string]any{"vigie.ignore": "true", "vigie.helper": "true"}
	if old, ok := cfg["Labels"].(map[string]any); ok {
		for k, v := range old {
			if !strings.HasPrefix(k, "com.docker.compose.") {
				labels[k] = v
			}
		}
	}
	cfg["Labels"] = labels
	cfg["Healthcheck"] = map[string]any{"Test": []any{"NONE"}}

	hc := copyMap(self.HostConfig)
	hc["AutoRemove"] = true
	hc["RestartPolicy"] = map[string]any{"Name": "no"}
	delete(hc, "PortBindings")
	hc["PublishAllPorts"] = false
	cfg["HostConfig"] = hc

	mode := str(hc, "NetworkMode")
	if !strings.HasPrefix(mode, "container:") && mode != "host" && mode != "none" {
		// Un seul réseau suffit pour joindre un éventuel socket-proxy ; pas d'alias pour ne pas
		// détourner le trafic destiné à l'agent.
		if ep, ok := self.NetworkSettings.Networks[mode]; ok {
			c := endpointConfig(ep, self.ID)
			delete(c, "Aliases")
			cfg["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{mode: c}}
		}
	}
	return cfg
}
