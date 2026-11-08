package core

// Compatibility helpers for the older delay tests, which used the pre-0.9.3
// boolean init helper and a non-pointer setup default. Both compile here
// only in tests; production callers use handleInit / SetupParams.
func handleInitClash(params *InitParams) bool {
	return handleInit(params) == nil
}

func defaultSetupParams() *SetupParams {
	return &SetupParams{}
}
