// Package converters holds the V11-to-V12 row converters. Each converter
// calls importer.Register in its init() function; the cmd packages blank-import
// this package to trigger that registration.
package converters
