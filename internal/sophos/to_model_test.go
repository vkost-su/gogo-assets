package sophos

import (
	"testing"

	"gogo-assets/internal/model"
)

func TestToEndpointPointerDiscipline(t *testing.T) {
	t.Run("tamper false → non-nil *false", func(t *testing.T) {
		e := ToEndpoint(Endpoint{EndpointID: "e1", TamperProtected: false}, model.Meta{})
		if e.TamperProtection == nil || *e.TamperProtection != false {
			t.Errorf("TamperProtection = %v, want non-nil *false", e.TamperProtection)
		}
	})

	t.Run("online true → non-nil *true", func(t *testing.T) {
		e := ToEndpoint(Endpoint{EndpointID: "e1", Online: true}, model.Meta{})
		if e.Online == nil || *e.Online != true {
			t.Errorf("Online = %v, want non-nil *true", e.Online)
		}
	})
}

func TestToAccountHealth(t *testing.T) {
	if h := ToAccountHealth(nil, model.Meta{}); h != nil {
		t.Errorf("empty endpoints → %+v, want nil", h)
	}
	endpoints := []Endpoint{
		{HealthOverall: "good", TamperProtected: true, AlertCount: 0},
		{HealthOverall: "bad", TamperProtected: false, AlertCount: 3},
		{HealthOverall: "", TamperProtected: true, AlertCount: 1}, // unknown
	}
	h := ToAccountHealth(endpoints, model.Meta{})
	if h.EndpointsTotal != 3 || h.HealthGood != 1 || h.HealthBad != 1 || h.HealthUnknown != 1 {
		t.Errorf("rollup counts wrong: %+v", h)
	}
	if h.TamperOffCount != 1 || h.TotalAlerts != 4 {
		t.Errorf("tamper/alerts wrong: tamperOff=%d alerts=%d", h.TamperOffCount, h.TotalAlerts)
	}
}
