package server

// WithAssemblyCounter installs a test hook that fires once per *real*
// comparison assembly.
//
// It exists so the single-flight property can be asserted by counting work
// rather than by timing: "N concurrent identical requests did one unit of
// work" is a fact, while "the second request was fast" is a race.
func WithAssemblyCounter(opts *Options, count func()) {
	opts.onComparisonAssembled = count
}
