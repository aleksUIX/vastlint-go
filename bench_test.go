package vastlint

import (
	"context"
	"os"
	"testing"
)

func benchFixture(b *testing.B, name string) string {
	xml, err := os.ReadFile("testdata/fixtures/" + name)
	if err != nil {
		b.Fatal(err)
	}
	return string(xml)
}

func benchEngine(b *testing.B) Engine {
	e, err := New(context.Background(), Config{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { e.Close() })
	return e
}

func BenchmarkValidateSmall(b *testing.B) {
	e := benchEngine(b)
	xml := benchFixture(b, "err_inline_missing_impression.xml")
	b.SetBytes(int64(len(xml)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := e.Validate(context.Background(), xml, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateLarge(b *testing.B) {
	e := benchEngine(b)
	xml := benchFixture(b, "warn_publica_large.xml")
	b.SetBytes(int64(len(xml)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := e.Validate(context.Background(), xml, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateSmallParallel(b *testing.B) {
	e := benchEngine(b)
	xml := benchFixture(b, "err_inline_missing_impression.xml")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := e.Validate(context.Background(), xml, Options{}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
