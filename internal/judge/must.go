package judge

// must returns v, and panics if err isn't nil. Use it for errors that only a
// bug in the judge can cause, such as an error from the marshal of the
// judge's own data. A returned error would make all callers more complex.
func must[V any](v V, err error) V {
	if err != nil {
		panic(err)
	}
	return v
}
