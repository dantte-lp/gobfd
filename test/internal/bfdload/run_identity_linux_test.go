//go:build linux

package bfdload

import "testing"

func TestControlIdentityDistinguishesRunsAndBindsSameDirectory(t *testing.T) {
	t.Parallel()
	directory := privateControlDirectory(t)
	first, err := OpenControl(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	}()
	for index, path := range []string{directory, privateControlDirectory(t)} {
		other, err := OpenControl(path)
		if err != nil {
			t.Fatal(err)
		}
		if !first.run.valid() || !other.run.valid() || other.run.BootID != first.run.BootID ||
			(other.run == first.run) != (index == 0) {
			t.Errorf("control directory failed to bind the actual boot/run: first=%+v other=%+v", first.run, other.run)
		}
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}
}
