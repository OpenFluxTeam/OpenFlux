# OpenFlux patch to pion/sctp v1.11.3

Upstream: https://github.com/pion/sctp (MIT, see LICENSE). Tests and examples are left out of this copy.

One change, in `association.go`: the default MTU for outgoing packets (`initialMTU`, 1191) became a variable with
an exported setter, `SetInitialMTU`. `pion/webrtc` builds its SCTP association without passing an MTU, so there
was no way to make the packets of a data channel fuller; the OpenFlux script host calls the setter before it
opens a connection (see `transport/script/host_webrtc.go`).

To drop this copy: pion/webrtc would have to expose `sctp.WithMTU` through its `SettingEngine`.
