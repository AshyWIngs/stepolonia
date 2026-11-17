package content

// Test-only bridges let external contract tests use the production validators
// without exposing a new runtime validation or authorization entry point.
var DocumentationInitialType = initialType
var DocumentationValidateValue = validateValue
var DocumentationValidateValues = validateValues
