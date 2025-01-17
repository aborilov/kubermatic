// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// BIOS If set (default), BIOS will be used.
//
// swagger:model BIOS
type BIOS struct {

	// If set, the BIOS output will be transmitted over serial
	// +optional
	UseSerial bool `json:"useSerial,omitempty"`
}

// Validate validates this b i o s
func (m *BIOS) Validate(formats strfmt.Registry) error {
	return nil
}

// ContextValidate validates this b i o s based on context it is used
func (m *BIOS) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	return nil
}

// MarshalBinary interface implementation
func (m *BIOS) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *BIOS) UnmarshalBinary(b []byte) error {
	var res BIOS
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
