// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// AgentPoolUpgradeSettings agent pool upgrade settings
//
// swagger:model AgentPoolUpgradeSettings
type AgentPoolUpgradeSettings struct {

	// MaxSurge - This can either be set to an integer (e.g. '5') or a percentage (e.g. '50%'). If a percentage is specified, it is the percentage of the total agent pool size at the time of the upgrade. For percentages, fractional nodes are rounded up. If not specified, the default is 1. For more information, including best practices, see: https://docs.microsoft.com/azure/aks/upgrade-cluster#customize-node-surge-upgrade
	MaxSurge string `json:"maxSurge,omitempty"`
}

// Validate validates this agent pool upgrade settings
func (m *AgentPoolUpgradeSettings) Validate(formats strfmt.Registry) error {
	return nil
}

// ContextValidate validates this agent pool upgrade settings based on context it is used
func (m *AgentPoolUpgradeSettings) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	return nil
}

// MarshalBinary interface implementation
func (m *AgentPoolUpgradeSettings) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *AgentPoolUpgradeSettings) UnmarshalBinary(b []byte) error {
	var res AgentPoolUpgradeSettings
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
