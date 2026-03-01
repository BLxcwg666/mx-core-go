// Package comment implements the comment module (CRUD, replies, admin actions).
//
// Files in this package:
//   - types.go   — DTOs, response structs, sentinel errors, constants
//   - service.go — Service struct and all business-logic methods
//   - handler.go — Handler struct, route registration, and HTTP handlers
//   - helpers.go — Pure utility/helper functions (normalization, compact refs, etc.)
package comment
