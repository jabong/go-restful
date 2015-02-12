package restful

// Copyright 2013 Ernest Micklei. All rights reserved.
// Use of this source code is governed by a license
// that can be found in the LICENSE file.

import (
    "bytes"
    "encoding/json"
    "encoding/xml"
    "errors"
    "io"
    "io/ioutil"
    "net/http"
    "reflect"
    "strings"
)

var defaultRequestContentType string

var doCacheReadEntityBytes = true

// Request is a wrapper for a http Request that provides convenience methods
type Request struct {
    Request           *http.Request
    bodyContent       *[]byte // to cache the request body for multiple reads of ReadEntity
    pathParameters    map[string]string
    attributes        map[string]interface{} // for storing request-scoped values
    selectedRoutePath string                 // root path + route path that matched the request, e.g. /meetings/{id}/attendees
}

func NewRequest(httpRequest *http.Request) *Request {
    return &Request{
        Request:        httpRequest,
        pathParameters: map[string]string{},
        attributes:     map[string]interface{}{},
    }   // empty parameters, attributes
}

// If ContentType is missing or */* is given then fall back to this type, otherwise
// a "Unable to unmarshal content of type:" response is returned.
// Valid values are restful.MIME_JSON and restful.MIME_XML
// Example:
// 	restful.DefaultRequestContentType(restful.MIME_JSON)
func DefaultRequestContentType(mime string) {
    defaultRequestContentType = mime
}

// SetCacheReadEntity controls whether the response data ([]byte) is cached such that ReadEntity is repeatable.
// Default is true (due to backwardcompatibility). For better performance, you should set it to false if you don't need it.
func SetCacheReadEntity(doCache bool) {
    doCacheReadEntityBytes = doCache
}

// PathParameter accesses the Path parameter value by its name
func (r *Request) PathParameter(name string) string {
    return r.pathParameters[name]
}

// PathParameters accesses the Path parameter values
func (r *Request) PathParameters() map[string]string {
    return r.pathParameters
}

// QueryParameter returns the (first) Query parameter value by its name
func (r *Request) QueryParameter(name string) string {
    return r.Request.FormValue(name)
}

// BodyParameter parses the body of the request (once for typically a POST or a PUT) and returns the value of the given name or an error.
func (r *Request) BodyParameter(name string) (string, error) {
    err := r.Request.ParseForm()
    if err != nil {
        return "", err
    }
    return r.Request.PostFormValue(name), nil
}

// HeaderParameter returns the HTTP Header value of a Header name or empty if missing
func (r *Request) HeaderParameter(name string) string {
    return r.Request.Header.Get(name)
}

// ReadEntity checks the Accept header and reads the content into the entityPointer
// May be called multiple times in the request-response flow
func (r *Request) ReadEntity(entityPointer interface{}) (err error) {
    contentType := r.Request.Header.Get(HEADER_ContentType)
    if doCacheReadEntityBytes {
        return r.cachingReadEntity(contentType, entityPointer)
    }
    // unmarshall directly from request Body
    return r.decodeEntity(r.Request.Body, contentType, entityPointer)
}

func (r *Request) cachingReadEntity(contentType string, entityPointer interface{}) (err error) {
    var buffer []byte
    if r.bodyContent != nil {
        buffer = *r.bodyContent
    } else {
        buffer, err = ioutil.ReadAll(r.Request.Body)
        if err != nil {
            return err
        }
        r.bodyContent = &buffer
    }
    return r.decodeEntity(bytes.NewReader(buffer), contentType, entityPointer)
}

func (r *Request) decodeEntity(reader io.Reader, contentType string, entityPointer interface{}) (err error) {
    if strings.Contains(contentType, MIME_XML) {
        err := xml.NewDecoder(reader).Decode(entityPointer)
        if err != nil {
            return err
        }
        return Validate(entityPointer)
    }
    if strings.Contains(contentType, MIME_JSON) || MIME_JSON == defaultRequestContentType {
        decoder := json.NewDecoder(reader)
        decoder.UseNumber()
        err := decoder.Decode(entityPointer)
        if err != nil {
            return err
        }

        return Validate(entityPointer)
    }
    if MIME_XML == defaultRequestContentType {
        err := xml.NewDecoder(reader).Decode(entityPointer)
        if err != nil {
            return err
        }
        return Validate(entityPointer)
    }

    return NewError(400, "Unable to unmarshal content of type:"+contentType)
}

// SetAttribute adds or replaces the attribute with the given value.
func (r *Request) SetAttribute(name string, value interface{}) {
    r.attributes[name] = value
}

// Attribute returns the value associated to the given name. Returns nil if absent.
func (r Request) Attribute(name string) interface{} {
    return r.attributes[name]
}

// SelectedRoutePath root path + route path that matched the request, e.g. /meetings/{id}/attendees
func (r Request) SelectedRoutePath() string {
    return r.selectedRoutePath
}

// Validate input based upon binding tag of struct
func Validate(obj interface{}, parents ...string) error {
    typ := reflect.TypeOf(obj)
    val := reflect.ValueOf(obj)

    if typ.Kind() == reflect.Ptr {
        typ = typ.Elem()
        val = val.Elem()
    }

    switch typ.Kind() {
    case reflect.Struct:
        for i := 0; i < typ.NumField(); i++ {
            field := typ.Field(i)

            // Allow ignored fields in the struct
            if field.Tag.Get("form") == "-" {
                continue
            }

            fieldValue := val.Field(i).Interface()
            zero := reflect.Zero(field.Type).Interface()

            if strings.Index(field.Tag.Get("binding"), "required") > -1 {
                fieldType := field.Type.Kind()
                if fieldType == reflect.Struct {
                    if reflect.DeepEqual(zero, fieldValue) {
                        return errors.New("Required " + field.Tag.Get("json"))
                    }
                    err := Validate(fieldValue, field.Name)
                    if err != nil {
                        return err
                    }
                } else if reflect.DeepEqual(zero, fieldValue) {
                    if len(parents) > 0 {
                        return errors.New("Required " + field.Tag.Get("json") + " on " + parents[0])
                    } else {
                        return errors.New("Required " + field.Tag.Get("json"))
                    }
                } else if fieldType == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct {
                    err := Validate(fieldValue)
                    if err != nil {
                        return err
                    }
                }
            }
        }
    case reflect.Slice:
        for i := 0; i < val.Len(); i++ {
            fieldValue := val.Index(i).Interface()
            err := Validate(fieldValue)
            if err != nil {
                return err
            }
        }
    default:
        return nil
    }
    return nil
}
