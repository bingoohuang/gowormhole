package util

import (
	"encoding/json"
	"fmt"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var unmarshalledJson interface{}
	if err := json.Unmarshal(b, &unmarshalledJson); err != nil {
		return err
	}

	switch value := unmarshalledJson.(type) {
	case float64:
		*d = Duration(value)
	case string:
		pd, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(pd)
	default:
		return fmt.Errorf("invalid duration: %#v", unmarshalledJson)
	}

	return nil
}

func (d Duration) MarshalJSON() (b []byte, err error) {
	return []byte(fmt.Sprintf(`"%s"`, time.Duration(d).String())), nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }
