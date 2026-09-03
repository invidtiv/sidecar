package styles

import (
	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// SidecarModernSyntaxThemeName is the identifier for the custom Chroma style
// tuned to the Sidecar Modern launch visual language.
const SidecarModernSyntaxThemeName = "sidecar-modern"

// SidecarModernChromaStyle defines the syntax highlighting style for the
// sidecar-modern theme. It uses red for keywords, teal/blue-green (matching the
// directory names in the files plugin) for types, builtins, and namespaces, gold
// for function names and constants, sage green for strings, lavender for
// numbers, and muted neutrals for operators and comments.
var SidecarModernChromaStyle = chroma.MustNewStyle(SidecarModernSyntaxThemeName, chroma.StyleEntries{
	chroma.Background: "bg:#0f1113 #cfd3d6",
	chroma.Text:       "#cfd3d6",

	// Comments
	chroma.Comment:            "#697177",
	chroma.CommentHashbang:    "#697177",
	chroma.CommentMultiline:   "#697177",
	chroma.CommentSingle:      "#697177",
	chroma.CommentSpecial:     "bold #c0982f",
	chroma.CommentPreproc:     "#4a8f8f",
	chroma.CommentPreprocFile: "#7fae86",

	// Keywords & declarations (red)
	chroma.Keyword:            "#c06c64",
	chroma.KeywordDeclaration: "#c06c64",
	chroma.KeywordNamespace:   "#c06c64",
	chroma.KeywordPseudo:      "#c06c64",
	chroma.KeywordReserved:    "#c06c64",
	chroma.KeywordConstant:    "#c0982f",
	chroma.KeywordType:        "#4a8f8f",

	// Types, namespaces, builtins (teal / blue-green)
	chroma.NameClass:         "#4a8f8f",
	chroma.NameBuiltin:       "#4a8f8f",
	chroma.NameBuiltinPseudo: "#4a8f8f",
	chroma.NameNamespace:     "#4a8f8f",
	chroma.NameAttribute:     "#4a8f8f",
	chroma.NameVariableClass: "#4a8f8f",

	// Functions & methods (signature gold accent)
	chroma.NameFunction:      "#c0982f",
	chroma.NameFunctionMagic: "#c0982f",
	chroma.NameDecorator:     "#a57fb9",

	// Identifiers, variables, properties
	chroma.Name:                  "#cfd3d6",
	chroma.NameVariable:          "#cfd3d6",
	chroma.NameVariableGlobal:    "#cfd3d6",
	chroma.NameVariableInstance:  "#cfd3d6",
	chroma.NameVariableAnonymous: "#cfd3d6",
	chroma.NameVariableMagic:     "#c0982f",
	chroma.NameProperty:          "#cfd3d6",
	chroma.NameOther:             "#cfd3d6",
	chroma.NameConstant:          "#c0982f",
	chroma.NameEntity:            "#c0982f",
	chroma.NameTag:               "#c06c64",
	chroma.NameLabel:             "#c06c64",
	chroma.NameException:         "#c06c64",
	chroma.NameKeyword:           "#c06c64",

	// Strings (sage green matching DiffAdd / Success)
	chroma.LiteralString:          "#7fae86",
	chroma.LiteralStringAffix:     "#c06c64",
	chroma.LiteralStringAtom:      "#7fae86",
	chroma.LiteralStringBacktick:  "#7fae86",
	chroma.LiteralStringBoolean:   "#c0982f",
	chroma.LiteralStringChar:      "#7fae86",
	chroma.LiteralStringDelimiter: "#8b9298",
	chroma.LiteralStringDoc:       "#858e95",
	chroma.LiteralStringDouble:    "#7fae86",
	chroma.LiteralStringEscape:    "#4a8f8f",
	chroma.LiteralStringHeredoc:   "#7fae86",
	chroma.LiteralStringInterpol:  "#c0982f",
	chroma.LiteralStringName:      "#7fae86",
	chroma.LiteralStringOther:     "#7fae86",
	chroma.LiteralStringRegex:     "#c97a72",
	chroma.LiteralStringSingle:    "#7fae86",
	chroma.LiteralStringSymbol:    "#c0982f",

	// Numbers & dates (lavender / purple)
	chroma.LiteralNumber:            "#a57fb9",
	chroma.LiteralNumberBin:         "#a57fb9",
	chroma.LiteralNumberFloat:       "#a57fb9",
	chroma.LiteralNumberHex:         "#a57fb9",
	chroma.LiteralNumberInteger:     "#a57fb9",
	chroma.LiteralNumberIntegerLong: "#a57fb9",
	chroma.LiteralNumberOct:         "#a57fb9",
	chroma.LiteralDate:              "#a57fb9",
	chroma.LiteralOther:             "#7fae86",

	// Operators & punctuation (neutral secondary)
	chroma.Operator:     "#8b9298",
	chroma.OperatorWord: "#c06c64",
	chroma.Punctuation:  "#8b9298",

	// Generic formatting & diff markup
	chroma.GenericDeleted:    "#c97a72",
	chroma.GenericEmph:       "italic",
	chroma.GenericError:      "#c06c64",
	chroma.GenericHeading:    "bold #4b8fd6",
	chroma.GenericInserted:   "#7fae86",
	chroma.GenericOutput:     "#8b9298",
	chroma.GenericPrompt:     "#c0982f",
	chroma.GenericStrong:     "bold",
	chroma.GenericSubheading: "bold #4a8f8f",
	chroma.GenericTraceback:  "#c06c64",
	chroma.GenericUnderline:  "underline",
})

func init() {
	chromastyles.Register(SidecarModernChromaStyle)
}
