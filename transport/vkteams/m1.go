package vkteams

// Exported surface for the M1 lab harness (cmd/vkteamsm1): a single media lane
// over a freshly joined call. The shipped transport mints its lanes through a
// group (one media lane plus the raw-allocation lanes) and does not go through
// here; this exists only so the measurement tool can drive one lane directly.

// M1Lane is one media lane plus the call it joined.
type M1Lane struct {
	*mediaLane
	cc *callCtx
}

// DialM1Lane joins the call and starts one media lane in the given role.
func DialM1Lane(code string, isExit, forceRelay bool) (*M1Lane, error) {
	cc, err := joinCall(code, isExit)
	if err != nil {
		return nil, err
	}
	ml, err := newMediaLane(cc, forceRelay)
	if err != nil {
		cc.bus.close()
		return nil, err
	}
	if err := ml.Start(); err != nil {
		cc.bus.close()
		return nil, err
	}
	return &M1Lane{mediaLane: ml, cc: cc}, nil
}

// Stop tears down the lane and closes the shared signalling bus.
func (m *M1Lane) Stop() error {
	err := m.mediaLane.Stop()
	m.cc.bus.close()
	return err
}
