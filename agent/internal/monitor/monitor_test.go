package monitor

import "testing"

func TestProblemOf(t *testing.T) {
	cases := []struct {
		c    Container
		want string
	}{
		{Container{State: "running"}, ""},
		{Container{State: "running", Health: "unhealthy"}, "unhealthy"},
		{Container{State: "restarting"}, "restarting"},
		{Container{State: "exited", ExitCode: 0}, ""},
		{Container{State: "exited", ExitCode: 143}, ""},
		{Container{State: "exited", ExitCode: 1}, "down"},
		{Container{State: "exited", ExitCode: 1, StoppedByUser: true}, ""},
		{Container{State: "exited", ExitCode: 137, OOMKilled: true}, "oom"},
		{Container{State: "exited", ExitCode: 2, Ignored: true}, ""},
	}
	for _, tc := range cases {
		if got := problemOf(&tc.c); got != tc.want {
			t.Errorf("%+v : %q, attendu %q", tc.c, got, tc.want)
		}
	}
}
