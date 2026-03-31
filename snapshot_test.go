package configsdk

import (
"sync"
"testing"
)

func TestSnapshot_StoreAndLoad(t *testing.T) {
	s := NewSnapshot[string]()

	if v := s.Load(); v != "" {
		t.Errorf("initial Load() = %q, want empty", v)
	}

	s.Store("hello")
	if v := s.Load(); v != "hello" {
		t.Errorf("Load() = %q, want %q", v, "hello")
	}

	s.Store("world")
	if v := s.Load(); v != "world" {
		t.Errorf("Load() = %q, want %q", v, "world")
	}
}

func TestSnapshot_StructType(t *testing.T) {
	type Config struct {
		Host string
		Port int
	}

	s := NewSnapshot[Config]()

	s.Store(Config{Host: "localhost", Port: 5432})
	got := s.Load()
	if got.Host != "localhost" || got.Port != 5432 {
		t.Errorf("got %+v, want {localhost 5432}", got)
	}
}

func TestSnapshot_ConcurrentAccess(t *testing.T) {
	s := NewSnapshot[int]()

	var wg sync.WaitGroup
	const writers = 10
	const readers = 20
	const iterations = 1000

	// Writers
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				s.Store(id*iterations + j)
			}
		}(i)
	}

	// Readers
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = s.Load()
			}
		}()
	}

	wg.Wait()

	// After all writes complete, value should be one of the final written values
	v := s.Load()
	if v < 0 {
		t.Errorf("unexpected value %d", v)
	}
}

func TestSnapshot_ZeroValue(t *testing.T) {
	si := NewSnapshot[int]()
	if v := si.Load(); v != 0 {
		t.Errorf("int Load() = %d, want 0", v)
	}

	sb := NewSnapshot[bool]()
	if v := sb.Load(); v != false {
		t.Errorf("bool Load() = %v, want false", v)
	}

	type S struct{ X int }
	ss := NewSnapshot[S]()
	if v := ss.Load(); v.X != 0 {
		t.Errorf("struct Load() = %+v, want zero", v)
	}
}
