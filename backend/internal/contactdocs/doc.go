// Package contactdocs stores an optional CPF or CNPJ for a contact.
// The document is not required to open a conversation and never authenticates
// a person. Plaintext is sealed with the versioned master key. Exact search
// uses a tenant-scoped HMAC and does not keep a plaintext index.
package contactdocs
