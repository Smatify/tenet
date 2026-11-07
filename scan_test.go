package tenet

import "testing"

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{"ID": "id", "LastAt": "last_at", "ThreadID": "thread_id", "N": "n", "HTMLBody": "html_body"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%s) = %s, want %s", in, got, want)
		}
	}
}
