package configchecks

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// checkConfiguration re-reads the config file from disk. The running app
// loaded one at startup by definition, but a user (or an agent) may have edited
// the file since, and the next save would be made against a file Sidecar can no
// longer parse. Reading it fresh is the whole point of the check.
func checkConfiguration(in Input) Result {
	path := in.configPath()
	result := Result{ID: CheckConfiguration, Title: "Configuration"}

	env := in.env()
	content, err := env.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No file is a valid state: Sidecar runs on defaults until
			// something is saved. Saying so is more useful than a warning.
			result.OK = true
			result.Summary = "Using defaults · no config file yet"
			result.Evidence = []string{path + " does not exist."}
			return result
		}
		result.Summary = "Could not be read · open the error and repair safely"
		result.Evidence = []string{path, err.Error()}
		result.Action = "Repair configuration"
		result.ActionDetail = "Sidecar could not read your configuration file"
		result.Badge = BadgeFix
		result.Repair = RepairConfiguration
		return result
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(content, &probe); err != nil {
		result.Summary = "A setting could not be read · open the error and repair safely"
		result.Evidence = []string{path, err.Error()}
		if line := jsonErrorContext(content, err); line != "" {
			result.Evidence = append(result.Evidence, line)
		}
		result.Action = "Repair configuration"
		result.ActionDetail = "Sidecar could not parse your configuration file"
		result.Badge = BadgeFix
		result.Repair = RepairConfiguration
		return result
	}

	result.OK = true
	result.Summary = "Readable and valid"
	result.Evidence = []string{path}
	return result
}

// jsonErrorContext turns a byte offset into the line the user has to look at.
// A repair the user performs in an editor is only as good as the location it
// hands them.
func jsonErrorContext(content []byte, err error) string {
	offset := int64(-1)
	switch typed := err.(type) {
	case *json.SyntaxError:
		offset = typed.Offset
	case *json.UnmarshalTypeError:
		offset = typed.Offset
	}
	if offset < 0 || offset > int64(len(content)) {
		return ""
	}
	line := 1 + strings.Count(string(content[:offset]), "\n")
	return "Near line " + strconv.Itoa(line)
}
