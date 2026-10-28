package store

import (
	"context"
	"testing"
	"time"
)

func TestTop(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	// Deux tranches complètes de 15 min : comet alterne 10 et 20 % de CPU, 1 ko/s reçu, 500 o/s émis.
	base := time.Now().Add(-3 * time.Hour).Truncate(15 * time.Minute)
	var samples []CtrSample
	for i := 0; i < 30; i++ {
		ts := base.Add(time.Duration(i) * time.Minute).UnixMilli()
		samples = append(samples,
			CtrSample{Name: "comet", TS: ts, CPU: float64(10 + 10*(i%2)), MemBytes: 100 << 20, Rx: 1000, Tx: 500},
			CtrSample{Name: "redis", TS: ts, CPU: 1, MemBytes: 50 << 20, Rx: 10, Tx: 20},
		)
	}
	if err := st.InsertCtr(ctx, samples); err != nil {
		t.Fatal(err)
	}
	if err := st.Aggregate(ctx, 4*time.Hour); err != nil {
		t.Fatal(err)
	}
	want := []Consumer{
		{Name: "comet", CPUAvg: 15, CPUMax: 20, MemAvg: 100 << 20, RxBytes: 1_800_000, TxBytes: 900_000},
		{Name: "redis", CPUAvg: 1, CPUMax: 1, MemAvg: 50 << 20, RxBytes: 18_000, TxBytes: 36_000},
	}
	for _, rg := range []string{"24h", "7d"} {
		r, _ := ParseRange(rg)
		got, err := st.Top(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s : %+v, attendu %+v", rg, got, want)
		}
	}
}
