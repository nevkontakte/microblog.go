package judge

// must returns v, and panics if err isn't nil. It is for errors that only a
// bug in the judge can cause, such as marshaling the judge's own data, where
// returning them would burden every caller.
func must[V any](v V, err error) V {
	if err != nil {
		panic(err)
	}
	return v
}
