package store

import (
	"context"
	"testing"
	"time"
)

func TestVolumeSizesRetention(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour
	for _, ago := range []time.Duration{31 * day, 29 * day, time.Hour} {
		if err := st.InsertVolumeSizes(ctx, now.Add(-ago).UnixMilli(), map[string]int64{"a": int64(ago / time.Hour), "b": 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PruneVolumeSizes(ctx, now); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Réouverture : la migration de la table est rejouée sans erreur, les mesures récentes restent.
	if st, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := st.VolumeHistory(ctx, now.Add(-60*day))
	if err != nil {
		t.Fatal(err)
	}
	if len(h["a"]) != 2 || len(h["b"]) != 2 || h["a"][0].Size != 29*24 || h["a"][1].Size != 1 {
		t.Fatalf("après 30 jours de rétention : %+v", h)
	}
}
