package store

import (
	"context"
	"fmt"
	"testing"
)

func TestVulnCache(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// Fiches : plus de 500 identifiants (requêtes découpées), mise à jour puis nettoyage par date.
	var rows []VulnRecord
	var ids []string
	for i := range 1200 {
		id := fmt.Sprintf("GHSA-%04d", i)
		ids = append(ids, id)
		rows = append(rows, VulnRecord{ID: id, Modified: "m1", Data: `{"id":"` + id + `"}`, FetchedAt: int64(i)})
	}
	if err := st.SetVulnRecords(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := st.SetVulnRecords(ctx, []VulnRecord{{ID: "GHSA-0001", Modified: "m2", Data: "{}", FetchedAt: 5000}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.VulnRecords(ctx, append(ids, "absent"))
	if err != nil || len(got) != 1200 || got["GHSA-0001"].Modified != "m2" || got["GHSA-1199"].FetchedAt != 1199 {
		t.Fatalf("%d fiches (%v) %+v", len(got), err, got["GHSA-0001"])
	}
	if err := st.PruneVulnRecords(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.VulnRecords(ctx, ids); len(got) != 201 || got["GHSA-0001"].ID == "" {
		t.Fatalf("après nettoyage : %d fiches", len(got))
	}

	// Inventaires : gardés pour les images encore utilisées.
	for _, id := range []string{"sha256:a", "sha256:b", "sha256:c"} {
		if err := st.SetVulnInventory(ctx, id, `{"v":1}`, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PruneVulnInventories(ctx, []string{"sha256:b"}); err != nil {
		t.Fatal(err)
	}
	if st.VulnInventory(ctx, "sha256:a") != "" || st.VulnInventory(ctx, "sha256:b") != `{"v":1}` {
		t.Fatal("inventaires mal nettoyés")
	}
	if err := st.PruneVulnInventories(ctx, nil); err != nil || st.VulnInventory(ctx, "sha256:b") != "" {
		t.Fatalf("aucune image : %v", err)
	}
}
