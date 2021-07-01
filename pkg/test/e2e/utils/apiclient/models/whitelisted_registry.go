// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// WhitelistedRegistry WhitelistedRegistry represents a object containing a whitelisted image registry prefix
//
// swagger:model WhitelistedRegistry
type WhitelistedRegistry struct {

	// name
	Name string `json:"name,omitempty"`

	// spec
	Spec *WhitelistedRegistrySpec `json:"spec,omitempty"`
}

// Validate validates this whitelisted registry
func (m *WhitelistedRegistry) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateSpec(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *WhitelistedRegistry) validateSpec(formats strfmt.Registry) error {

	if swag.IsZero(m.Spec) { // not required
		return nil
	}

	if m.Spec != nil {
		if err := m.Spec.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("spec")
			}
			return err
		}
	}

	return nil
}

// MarshalBinary interface implementation
func (m *WhitelistedRegistry) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *WhitelistedRegistry) UnmarshalBinary(b []byte) error {
	var res WhitelistedRegistry
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
