package main

import "github.com/prometheus/client_golang/prometheus"

var (
	rendezvousCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gowormhole",
			Name:      "rendezvous_attempts",
			Help:      "Number of attempts to rendezvous using the signalling server.",
		},
		[]string{"result"},
	)
	iceCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gowormhole",
			Name:      "webrtc_attempts",
			Help:      "Number of reported ICE results sliced by ICE method used.",
		},
		[]string{"result", "method"},
	)
	protocolErrorCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gowormhole",
			Name:      "protocol_errors",
			Help:      "Number of bad requests to the signalling server.",
		},
		[]string{"kind"},
	)
	slotsGuage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "gowormhole",
			Name:      "busy_slots",
			Help:      "Number of currently busy slots.",
		},
	)
)

func init() {
	prometheus.MustRegister(rendezvousCounter)
	prometheus.MustRegister(iceCounter)
	prometheus.MustRegister(protocolErrorCounter)
	prometheus.MustRegister(slotsGuage)
}
