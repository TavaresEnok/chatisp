package erp

import "context"

// SupplierEnabled reports whether an external ERP contract is configured.
// It stays false: this module does not invent a supplier endpoint.
func SupplierEnabled() bool { return false }

// supplier is the unwired real connector. Call does not open a network connection.
type supplier struct{}

func (supplier) Call(context.Context, Consult) (View, error) {
	return View{}, ErrDown
}

// Supplier returns the unwired connector. Runtime composition must not install it.
func Supplier() Adapter { return supplier{} }
