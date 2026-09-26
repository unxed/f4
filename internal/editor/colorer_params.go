//go:build !lite

package editor

import (
	"strings"
)

// ColorerTypeParams is one file type and its parameters, as FarColorer's HRC
// settings dialog shows them.
type ColorerTypeParams struct {
	Name        string
	Group       string
	Description string
	Params      []ColorerParam
}

// ColorerParam is one parameter of a file type.
type ColorerParam struct {
	Name        string
	Value       string // the value in effect
	Default     string // the value without the user's
	Description string
	UserSet     bool // the user has set a value
}

// LoadColorerTypeParams reads every file type and its parameters from a
// session of src, with the user's profile applied, and gives the session back
// to the pool. It is FarColorer's HRC settings dialog's data, read up front so
// the dialog holds no session while it is open.
func LoadColorerTypeParams(src ColorerSource) ([]ColorerTypeParams, error) {
	session, err := acquireColorerSession(src)
	if err != nil {
		return nil, err
	}
	defer releaseColorerSession(session, src)
	types, err := session.FileTypes()
	if err != nil {
		return nil, err
	}
	result := make([]ColorerTypeParams, 0, len(types))
	for _, t := range types {
		params, err := session.FileTypeParams(t.Name)
		if err != nil {
			return nil, err
		}
		entry := ColorerTypeParams{Name: t.Name, Group: t.Group, Description: t.Description}
		for _, p := range params {
			entry.Params = append(entry.Params, ColorerParam{Name: p.Name, Value: p.Value, Default: p.Default, Description: p.Description, UserSet: p.UserSet})
		}
		result = append(result, entry)
	}
	return result, nil
}

// SaveColorerParams records changes to the user's parameter values in
// HrcSettings.ini — a value, or nil to take the user's value back, by type and
// parameter — and drops pooled sessions, which still carry the old values.
func SaveColorerParams(changes map[string]map[string]*string) error {
	if len(changes) == 0 {
		return nil
	}
	profile := loadColorerProfile()
	for typeName, params := range changes {
		for param, value := range params {
			if value == nil {
				delete(profile[typeName], param)
				if len(profile[typeName]) == 0 {
					delete(profile, typeName)
				}
				continue
			}
			if profile[typeName] == nil {
				profile[typeName] = map[string]string{}
			}
			profile[typeName][param] = *value
		}
	}
	if err := saveColorerProfile(profile); err != nil {
		return err
	}
	ResetColorerSessions()
	return nil
}

// ColorerParamDefaultText is how FarColorer offers a parameter's default among
// its values: "<default-" + the default + ">". Picking it takes the user's
// value back.
func ColorerParamDefaultText(p ColorerParam) string {
	return "<default-" + p.Default + ">"
}

// ColorerParamChoices are the values FarEditorSet::OnChangeParam offers for a
// parameter, the default last, and whether only these may be picked. Numbers,
// colours and the hotkey are typed in.
func ColorerParamChoices(p ColorerParam) (choices []string, fixed bool) {
	switch p.Name {
	case "show-cross":
		choices, fixed = []string{"none", "vertical", "horizontal", "both"}, true
	case "cross-zorder":
		choices, fixed = []string{"bottom", "top"}, true
	case "maxlinelength", "backparse", "default-fore", "default-back", "firstlines", "firstlinebytes", "hotkey":
		choices, fixed = nil, false
	case "fullback":
		choices, fixed = []string{"yes", "no"}, true
	default:
		choices, fixed = []string{"true", "false"}, true
	}
	return append(choices, ColorerParamDefaultText(p)), fixed
}

// ColorerParamText is what the value field shows: the user's value, or the
// default text when there is none.
func ColorerParamText(p ColorerParam) string {
	if p.UserSet {
		return p.Value
	}
	return ColorerParamDefaultText(p)
}

// ColorerParamEdit is FarEditorSet::SaveChangedValueParam: from what the value
// field holds, the change to record — nil to take the user's value back — and
// whether there is one. p is updated to match.
func ColorerParamEdit(p *ColorerParam, text string) (value *string, changed bool) {
	text = strings.TrimSpace(text)
	if text == ColorerParamDefaultText(*p) {
		if !p.UserSet {
			return nil, false
		}
		p.UserSet, p.Value = false, p.Default
		return nil, true
	}
	if p.UserSet && p.Value == text {
		return nil, false
	}
	p.UserSet, p.Value = true, text
	return &text, true
}
