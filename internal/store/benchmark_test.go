package store

import (
	"fmt"
	"testing"
)

func newBenchStore(b *testing.B, compress bool) *Store {
	b.Helper()
	dir := b.TempDir()
	s, err := NewStore(dir, compress)
	if err != nil {
		b.Fatalf("NewStore: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

func makeData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return data
}

// ─── BenchmarkStorePut ──────────────────────────────────────────────────────

func BenchmarkStorePut(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			s := newBenchStore(b, false)
			data := makeData(sz.size)

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Vary content slightly per iteration so each Put creates a unique object
				data[0] = byte(i)
				data[1] = byte(i >> 8)
				_, _, err := s.Put(data)
				if err != nil {
					b.Fatalf("Put: %v", err)
				}
			}
		})
	}
}

// ─── BenchmarkStoreGet ──────────────────────────────────────────────────────

func BenchmarkStoreGet(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			s := newBenchStore(b, false)
			data := makeData(sz.size)
			hash, _, err := s.Put(data)
			if err != nil {
				b.Fatalf("Put: %v", err)
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := s.Get(hash)
				if err != nil {
					b.Fatalf("Get: %v", err)
				}
			}
		})
	}
}

// ─── BenchmarkStoreHas ──────────────────────────────────────────────────────

func BenchmarkStoreHas(b *testing.B) {
	s := newBenchStore(b, false)

	// Seed some objects
	var hashes []string
	for i := 0; i < 100; i++ {
		data := []byte(fmt.Sprintf("has-bench-content-%d", i))
		hash, _, err := s.Put(data)
		if err != nil {
			b.Fatalf("Put: %v", err)
		}
		hashes = append(hashes, hash)
	}

	b.Run("existing", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Has(hashes[i%len(hashes)])
		}
	})

	b.Run("missing", func(b *testing.B) {
		fakeHash := "0000000000000000000000000000000000000000000000000000000000000000"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Has(fakeHash)
		}
	})
}

// ─── BenchmarkStorePutCompressed vs Uncompressed ────────────────────────────

func BenchmarkStorePutCompressed(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			s := newBenchStore(b, true)
			data := makeData(sz.size)

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				data[0] = byte(i)
				data[1] = byte(i >> 8)
				_, _, err := s.Put(data)
				if err != nil {
					b.Fatalf("Put: %v", err)
				}
			}
		})
	}
}

func BenchmarkStorePutUncompressed(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			s := newBenchStore(b, false)
			data := makeData(sz.size)

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				data[0] = byte(i)
				data[1] = byte(i >> 8)
				_, _, err := s.Put(data)
				if err != nil {
					b.Fatalf("Put: %v", err)
				}
			}
		})
	}
}
