package nvclient

type Driver interface {
	//	Returns results search command.

	// Search:
	//	conditions:	flags like --get name=opsdb,id=1234 (map key is "get", value is slice of values comma delimited
	//	includes:	extra fields to include in search to opsdb
	//	fields:	fields to display to user
	Search(object_type string, conditions map[string][]string, includes []string, fields []string) (Result, error)
	// GetAllFields:	Returns all fields
	//	command:	flags like --get name=opsdb,id=1234 (map key is "get", value is slice of values comma delimited
	//	includes:	extra fields to include in search to opsdb
	//	flags:		fields to display to user
	GetAllFields(object_type string, command map[string][]string, includes []string, flags []string) (Result, error)

	// Set:
	//	conditions:	flags like --get name=opsdb,id=1234 (map key is "get", value is slice of values comma delimited
	//	includes:	extra fields to include in search to opsdb
	//	set:		fields to set and its value
	Set(object_type string, conditions map[string][]string, includes []string, set map[string]string) (string, error)

	GetAllSubsystemNames(objectType string) ([]string, error)

	SetServer(s string)
	GetServer() string
}