package swagger

import (
	"encoding/json"
	"reflect"
	"strings"
)

// ModelBuildable is used for extending Structs that need more control over
// how the Model appears in the Swagger api declaration.
type ModelBuildable interface {
	PostBuildModel(m *Model) *Model
}

type modelBuilder struct {
	Models map[string]Model
}

// addModelFrom creates and adds a Model to the builder and detects and calls
// the post build hook for customizations
func (b modelBuilder) addModelFrom(sample interface{}) {
	if modelOrNil := b.addModel(reflect.TypeOf(sample), ""); modelOrNil != nil {
		// allow customizations
		if buildable, ok := sample.(ModelBuildable); ok {
			modelOrNil = buildable.PostBuildModel(modelOrNil)
			b.Models[modelOrNil.Id] = *modelOrNil
		}
	}
}

func (b modelBuilder) addModel(st reflect.Type, nameOverride string) *Model {
	modelName := b.keyFrom(st)
	if nameOverride != "" {
		modelName = nameOverride
	}
	// no models needed for primitive types
	if b.isPrimitiveType(modelName) {
		return nil
	}
	// see if we already have visited this model
	if _, ok := b.Models[modelName]; ok {
		return nil
	}
	sm := Model{
		Id:         modelName,
		Required:   []string{},
		Properties: map[string]ModelProperty{}}

	// reference the model before further initializing (enables recursive structs)
	b.Models[modelName] = sm

	// check for slice or array
	if st.Kind() == reflect.Slice || st.Kind() == reflect.Array {
		b.addModel(st.Elem(), "")
		return &sm
	}
	// check for structure or primitive type
	if st.Kind() != reflect.Struct {
		return &sm
	}
	for i := 0; i < st.NumField(); i++ {
		field := st.Field(i)
		jsonName, prop := b.buildProperty(field, &sm, modelName)
		if descTag := field.Tag.Get("description"); descTag != "" {
			prop.Description = descTag
		}
		// add if not ommitted
		if len(jsonName) != 0 {
			// update Required
			if b.isPropertyRequired(field) {
				sm.Required = append(sm.Required, jsonName)
			}
			sm.Properties[jsonName] = prop
		}
	}
	// update model builder with completed model
	b.Models[modelName] = sm

	return &sm
}

func (b modelBuilder) isPropertyRequired(field reflect.StructField) bool {
	required := true
	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		s := strings.Split(jsonTag, ",")
		if len(s) > 1 && s[1] == "omitempty" {
			return false
		}
	}
	return required
}

func (b modelBuilder) buildProperty(field reflect.StructField, model *Model, modelName string) (jsonName string, prop ModelProperty) {
	jsonName = b.jsonNameOfField(field)
	if len(jsonName) == 0 {
		// empty name signals skip property
		return "", prop
	}
	fieldType := field.Type

	// check if type is doing its own marshalling
	marshalerType := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	if fieldType.Implements(marshalerType) {
		var pType = "string"
		prop.Type = &pType
		prop.Format = b.jsonSchemaFormat(fieldType.String())
		return jsonName, prop
	}

	// check if annotation says it is a string
	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		s := strings.Split(jsonTag, ",")
		if len(s) > 1 && s[1] == "string" {
			stringt := "string"
			prop.Type = &stringt
			return jsonName, prop
		}
	}

	fieldKind := fieldType.Kind()
	switch {
	case fieldKind == reflect.Struct:
		return b.buildStructTypeProperty(field, jsonName, model)
	case fieldKind == reflect.Slice || fieldKind == reflect.Array:
		return b.buildArrayTypeProperty(field, jsonName, modelName)
	case fieldKind == reflect.Ptr:
		return b.buildPointerTypeProperty(field, jsonName, modelName)
	case fieldKind == reflect.String:
		stringt := "string"
		prop.Type = &stringt
		return jsonName, prop
	case fieldKind == reflect.Map:
		// if it's a map, it's unstructured, and swagger 1.2 can't handle it
		anyt := "any"
		prop.Type = &anyt
		return jsonName, prop
	}

	if b.isPrimitiveType(fieldType.String()) {
		mapped := b.jsonSchemaType(fieldType.String())
		prop.Type = &mapped
		prop.Format = b.jsonSchemaFormat(fieldType.String())
		return jsonName, prop
	}
	modelType := fieldType.String()
	prop.Ref = &modelType

	if fieldType.Name() == "" { // override type of anonymous structs
		nestedTypeName := modelName + "." + jsonName
		prop.Ref = &nestedTypeName
		b.addModel(fieldType, nestedTypeName)
	}
	return jsonName, prop
}

func hasNamedJSONTag(field reflect.StructField) bool {
	parts := strings.Split(field.Tag.Get("json"), ",")
	if len(parts) == 0 {
		return false
	}
	for _, s := range parts[1:] {
		if s == "inline" {
			return false
		}
	}
	return len(parts[0]) > 0
}

func (b modelBuilder) buildStructTypeProperty(field reflect.StructField, jsonName string, model *Model) (nameJson string, prop ModelProperty) {
	fieldType := field.Type
	// check for anonymous
	if len(fieldType.Name()) == 0 {
		// anonymous
		anonType := model.Id + "." + jsonName
		b.addModel(fieldType, anonType)
		prop.Ref = &anonType
		return jsonName, prop
	}

	if field.Name == fieldType.Name() && field.Anonymous && !hasNamedJSONTag(field) {
		// embedded struct
		sub := modelBuilder{map[string]Model{}}
		sub.addModel(fieldType, "")
		subKey := sub.keyFrom(fieldType)
		// merge properties from sub
		subModel := sub.Models[subKey]
		for k, v := range subModel.Properties {
			model.Properties[k] = v
			// if subModel says this property is required then include it
			required := false
			for _, each := range subModel.Required {
				if k == each {
					required = true
					break
				}
			}
			if required {
				model.Required = append(model.Required, k)
			}
			// Add the model type to the global model list
			if v.Ref != nil {
				b.Models[*v.Ref] = sub.Models[*v.Ref]
			}
		}
		// empty name signals skip property
		return "", prop
	}
	// simple struct
	b.addModel(fieldType, "")
	var pType = fieldType.String()
	prop.Ref = &pType
	return jsonName, prop
}

func (b modelBuilder) buildArrayTypeProperty(field reflect.StructField, jsonName, modelName string) (nameJson string, prop ModelProperty) {
	fieldType := field.Type
	var pType = "array"
	prop.Type = &pType
	elemTypeName := b.getElementTypeName(modelName, jsonName, fieldType.Elem())
	prop.Items = new(Item)
	if b.isPrimitiveType(elemTypeName) {
		mapped := b.jsonSchemaType(elemTypeName)
		prop.Items.Type = &mapped
	} else {
		prop.Items.Ref = &elemTypeName
	}
	// add|overwrite model for element type
	if fieldType.Elem().Kind() == reflect.Ptr {
		fieldType = fieldType.Elem()
	}
	b.addModel(fieldType.Elem(), elemTypeName)
	return jsonName, prop
}

func (b modelBuilder) buildPointerTypeProperty(field reflect.StructField, jsonName, modelName string) (nameJson string, prop ModelProperty) {
	fieldType := field.Type

	// override type of pointer to list-likes
	if fieldType.Elem().Kind() == reflect.Slice || fieldType.Elem().Kind() == reflect.Array {
		var pType = "array"
		prop.Type = &pType
		elemName := b.getElementTypeName(modelName, jsonName, fieldType.Elem().Elem())
		prop.Items = &Item{Ref: &elemName}
		// add|overwrite model for element type
		b.addModel(fieldType.Elem().Elem(), elemName)
	} else {
		// non-array, pointer type
		var pType = fieldType.String()[1:] // no star, include pkg path
		prop.Ref = &pType
		elemName := ""
		if fieldType.Elem().Name() == "" {
			elemName = modelName + "." + jsonName
			prop.Ref = &elemName
		}
		b.addModel(fieldType.Elem(), elemName)
	}
	return jsonName, prop
}

func (b modelBuilder) getElementTypeName(modelName, jsonName string, t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		return t.String()[1:]
	}
	if t.Name() == "" {
		return modelName + "." + jsonName
	}
	if b.isPrimitiveType(t.Name()) {
		return b.jsonSchemaType(t.Name())
	}
	return b.keyFrom(t)
}

func (b modelBuilder) keyFrom(st reflect.Type) string {
	key := st.String()
	if len(st.Name()) == 0 { // unnamed type
		// Swagger UI has special meaning for [
		key = strings.Replace(key, "[]", "||", -1)
	}
	return key
}

// see also https://golang.org/ref/spec#Numeric_types
func (b modelBuilder) isPrimitiveType(modelName string) bool {
	return strings.Contains("uint8 uint16 uint32 uint64 int int8 int16 int32 int64 float32 float64 bool string byte rune time.Time", modelName)
}

// jsonNameOfField returns the name of the field as it should appear in JSON format
// An empty string indicates that this field is not part of the JSON representation
func (b modelBuilder) jsonNameOfField(field reflect.StructField) string {
	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		s := strings.Split(jsonTag, ",")
		if s[0] == "-" {
			// empty name signals skip property
			return ""
		} else if s[0] != "" {
			return s[0]
		}
	}
	return field.Name
}

// see also http://json-schema.org/latest/json-schema-core.html#anchor8
func (b modelBuilder) jsonSchemaType(modelName string) string {
	schemaMap := map[string]string{
		"uint8":  "integer",
		"uint16": "integer",
		"uint32": "integer",
		"uint64": "integer",

		"int":   "integer",
		"int8":  "integer",
		"int16": "integer",
		"int32": "integer",
		"int64": "integer",

		"byte":      "integer",
		"float64":   "number",
		"float32":   "number",
		"bool":      "boolean",
		"time.Time": "string",
	}
	mapped, ok := schemaMap[modelName]
	if !ok {
		return modelName // use as is (custom or struct)
	}
	return mapped
}

func (b modelBuilder) jsonSchemaFormat(modelName string) string {
	schemaMap := map[string]string{
		"int":       "int32",
		"int32":     "int32",
		"int64":     "int64",
		"byte":      "byte",
		"uint8":     "byte",
		"float64":   "double",
		"float32":   "float",
		"time.Time": "date-time",
	}
	mapped, ok := schemaMap[modelName]
	if !ok {
		return "" // no format
	}
	return mapped
}
