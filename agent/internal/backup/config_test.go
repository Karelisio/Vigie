package backup

import (
	"context"
	"strings"
	"testing"
)

func TestSecretsNeverReturned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	got := e.configure(t, Selection{ID: vigieID})
	if got.AccessKey != Mask || got.SecretKey != Mask || e.svc.Config(ctx).SecretKey != Mask {
		t.Fatalf("secrets non masqués : %+v", got)
	}
	if raw := e.st.Get(ctx, configKey); strings.Contains(raw, e.s3.secret) || strings.Contains(raw, e.s3.access) {
		t.Fatalf("secrets enregistrés avec la configuration : %s", raw)
	}

	// Valeur masquée ou vide : secrets conservés.
	for _, v := range []string{Mask, ""} {
		in := e.svc.Config(ctx)
		in.AccessKey, in.SecretKey, in.Retention = v, v, 5
		if _, err := e.svc.Update(ctx, in); err != nil {
			t.Fatal(err)
		}
		if _, sec := e.svc.load(ctx); sec.AccessKey != e.s3.access || sec.SecretKey != e.s3.secret {
			t.Fatalf("secrets perdus avec %q : %+v", v, sec)
		}
	}

	// Nouvelle adresse : la clé secrète doit être ressaisie.
	in := e.svc.Config(ctx)
	in.Endpoint = "https://autre.example.com"
	if _, err := e.svc.Update(ctx, in); err == nil || !strings.Contains(err.Error(), "saisis à nouveau la clé secrète") {
		t.Fatalf("nouvelle adresse sans clé : %v", err)
	}
	in.SecretKey = "nouvelle"
	if _, err := e.svc.Update(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, sec := e.svc.load(ctx); sec.SecretKey != "nouvelle" || sec.AccessKey != e.s3.access {
		t.Fatalf("secrets : %+v", sec)
	}
}

func TestConfigValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := e.configure(t)
	for _, c := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"heure", func(c *Config) { c.Time = "25:00" }, "heure invalide"},
		{"rétention", func(c *Config) { c.Retention = 0 }, "copies à garder"},
		{"schéma", func(c *Config) { c.Endpoint, c.SecretKey = "ftp://hôte", "s" }, "adresse du stockage invalide"},
		{"chemin", func(c *Config) { c.Endpoint, c.SecretKey = "https://hôte/bucket", "s" }, "adresse du stockage invalide"},
		{"bucket", func(c *Config) { c.Bucket = "mon bucket" }, "nom de bucket invalide"},
		{"préfixe", func(c *Config) { c.Prefix = "a/../b" }, "préfixe invalide"},
		{"élément", func(c *Config) { c.Items = []Selection{{ID: "bind:relatif"}} }, "élément invalide"},
		{"même dossier", func(c *Config) { c.Items = []Selection{{ID: "bind:/srv/a_b"}, {ID: "bind:/srv/a/b"}} }, "même dossier"},
		{"planification sans élément", func(c *Config) { c.Schedule = true }, "aucun élément"},
	} {
		in := base
		c.change(&in)
		if _, err := e.svc.Update(ctx, in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s : %v", c.name, err)
		}
	}
	in := base
	in.Prefix, in.Endpoint = "/serveurs/vps/", base.Endpoint+"/"
	out, err := e.svc.Update(ctx, in)
	if err != nil || out.Prefix != "serveurs/vps" || out.Endpoint != base.Endpoint {
		t.Fatalf("mise en forme : %+v %v", out, err)
	}

	// Planification sans stockage.
	fresh := newEnv(t)
	c := defaultConfig()
	c.Schedule, c.Items = true, []Selection{{ID: vigieID}}
	if _, err := fresh.svc.Update(ctx, c); err == nil || !strings.Contains(err.Error(), "stockage incomplet") {
		t.Fatalf("planification sans stockage : %v", err)
	}
}

func TestHistoryLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 0; i < maxHistory+5; i++ {
		e.svc.addHistory(ctx, Record{TS: int64(i), Items: []ItemResult{{ID: "volume:v", OK: true, Raw: int64(i)}}})
	}
	h := e.svc.History(ctx)
	if len(h) != maxHistory || h[0].TS != maxHistory+4 {
		t.Fatalf("historique : %d entrées, la plus récente %d", len(h), h[0].TS)
	}
	if lastRaw(h, "volume:v") != maxHistory+4 || lastRaw(h, "volume:autre") != 0 {
		t.Error("taille de la dernière copie")
	}
}
