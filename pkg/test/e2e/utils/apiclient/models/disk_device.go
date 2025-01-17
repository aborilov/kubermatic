// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// DiskDevice Represents the target of a volume to mount.
//
// Only one of its members may be specified.
//
// swagger:model DiskDevice
type DiskDevice struct {

	// cdrom
	Cdrom *CDRomTarget `json:"cdrom,omitempty"`

	// disk
	Disk *DiskTarget `json:"disk,omitempty"`

	// floppy
	Floppy *FloppyTarget `json:"floppy,omitempty"`

	// lun
	Lun *LunTarget `json:"lun,omitempty"`
}

// Validate validates this disk device
func (m *DiskDevice) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateCdrom(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateDisk(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateFloppy(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateLun(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *DiskDevice) validateCdrom(formats strfmt.Registry) error {
	if swag.IsZero(m.Cdrom) { // not required
		return nil
	}

	if m.Cdrom != nil {
		if err := m.Cdrom.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("cdrom")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) validateDisk(formats strfmt.Registry) error {
	if swag.IsZero(m.Disk) { // not required
		return nil
	}

	if m.Disk != nil {
		if err := m.Disk.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("disk")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) validateFloppy(formats strfmt.Registry) error {
	if swag.IsZero(m.Floppy) { // not required
		return nil
	}

	if m.Floppy != nil {
		if err := m.Floppy.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("floppy")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) validateLun(formats strfmt.Registry) error {
	if swag.IsZero(m.Lun) { // not required
		return nil
	}

	if m.Lun != nil {
		if err := m.Lun.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("lun")
			}
			return err
		}
	}

	return nil
}

// ContextValidate validate this disk device based on the context it is used
func (m *DiskDevice) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	var res []error

	if err := m.contextValidateCdrom(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateDisk(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateFloppy(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateLun(ctx, formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *DiskDevice) contextValidateCdrom(ctx context.Context, formats strfmt.Registry) error {

	if m.Cdrom != nil {
		if err := m.Cdrom.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("cdrom")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) contextValidateDisk(ctx context.Context, formats strfmt.Registry) error {

	if m.Disk != nil {
		if err := m.Disk.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("disk")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) contextValidateFloppy(ctx context.Context, formats strfmt.Registry) error {

	if m.Floppy != nil {
		if err := m.Floppy.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("floppy")
			}
			return err
		}
	}

	return nil
}

func (m *DiskDevice) contextValidateLun(ctx context.Context, formats strfmt.Registry) error {

	if m.Lun != nil {
		if err := m.Lun.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("lun")
			}
			return err
		}
	}

	return nil
}

// MarshalBinary interface implementation
func (m *DiskDevice) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *DiskDevice) UnmarshalBinary(b []byte) error {
	var res DiskDevice
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
