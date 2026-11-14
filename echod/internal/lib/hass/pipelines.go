package hass

import (
	"context"
	"encoding/json"
)

// Pipeline is one of Home Assistant's Assist pipelines, as its pipeline list names it.
type Pipeline struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Pipelines is Home Assistant's Assist pipelines and the id of the preferred one.
func (c *Client) Pipelines(ctx context.Context) (list []Pipeline, preferred string, err error) {
	raw, err := c.wsCall(ctx, map[string]any{"type": "assist_pipeline/pipeline/list"})
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Pipelines []Pipeline `json:"pipelines"`
		Preferred string     `json:"preferred_pipeline"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, "", err
	}
	return res.Pipelines, res.Preferred, nil
}
