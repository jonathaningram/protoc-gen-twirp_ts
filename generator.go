package main

import (
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
)

func samePackage(a *descriptor.FileDescriptorProto, b *descriptor.FileDescriptorProto) bool {
	if a.GetPackage() != b.GetPackage() {
		return false
	}
	if a.GetName() != b.GetName() {
		return false
	}
	return true
}

func fullTypeName(fd *descriptor.FileDescriptorProto, typeName string) string {
	return fmt.Sprintf(".%s.%s", fd.GetPackage(), typeName)
}

func generate(req *plugin.CodeGeneratorRequest) (*plugin.CodeGeneratorResponse, error) {
	resolver := dependencyResolver{}

	res := &plugin.CodeGeneratorResponse{
		File: []*plugin.CodeGeneratorResponse_File{
			{
				Name:    &twirpFileName,
				Content: &twirpSource,
			},
		},
	}

	protoFiles := req.GetProtoFile()
	for i := range protoFiles {
		file := protoFiles[i]

		pfile := &protoFile{
			Imports:  map[string]*importValues{},
			Messages: []*messageValues{},
			Services: []*serviceValues{},
			Enums:    []*enumValues{},
		}

		// Add enum
		for _, enum := range file.GetEnumType() {
			resolver.Set(file, enum.GetName())

			v := &enumValues{
				Name:   enum.GetName(),
				Values: []*enumKeyVal{},
			}

			for _, value := range enum.GetValue() {
				v.Values = append(v.Values, &enumKeyVal{
					Name:  value.GetName(),
					Value: value.GetNumber(),
				})
			}

			pfile.Enums = append(pfile.Enums, v)
		}

		// Add messages
		for _, message := range file.GetMessageType() {
			name := message.GetName()
			tsInterface := typeToInterface(name)
			jsonInterface := typeToJSONInterface(name)

			resolver.Set(file, name)
			resolver.Set(file, tsInterface)
			resolver.Set(file, jsonInterface)

			v := &messageValues{
				Name:          name,
				Interface:     tsInterface,
				JSONInterface: jsonInterface,

				Fields:      []*fieldValues{},
				NestedTypes: []*messageValues{},
				NestedEnums: []*enumValues{},
			}

			for _, m := message.GetMessageType() {
				// TODO: add support for nested messages
				// https://developers.google.com/protocol-buffers/docs/proto#nested
				log.Fatal("nested messages are not supported yet")
			}

			// Add nested enums
			for _, enum := range message.GetEnumType() {
				e := &enumValues{
					Name:   fmt.Sprintf("%s_%s", message.GetName(), enum.GetName()),
					Values: []*enumKeyVal{},
				}

				for _, value := range enum.GetValue() {
					e.Values = append(e.Values, &enumKeyVal{
						Name:  value.GetName(),
						Value: value.GetNumber(),
					})
				}

				v.NestedEnums = append(v.NestedEnums, e)
			}

			// Add message fields
			for _, field := range message.GetField() {
				fp, err := resolver.Resolve(field.GetTypeName())
				if err == nil {
					if !samePackage(fp, file) {
						pfile.Imports[fp.GetName()] = &importValues{
							Name: importName(fp),
							Path: importPath(file, fp.GetName()),
						}
					}
				}

				v.Fields = append(v.Fields, &fieldValues{
					Name:  field.GetName(),
					Field: camelCase(field.GetName()),

					Type:       resolver.TypeName(file, singularFieldType(field)),
					IsRepeated: isRepeated(field),
				})
			}

			pfile.Messages = append(pfile.Messages, v)
		}

		// Add services
		for _, service := range file.GetService() {
			resolver.Set(file, service.GetName())

			v := &serviceValues{
				Package:   file.GetPackage(),
				Name:      service.GetName(),
				Interface: typeToInterface(service.GetName()),
				Methods:   []*serviceMethodValues{},
			}

			for _, method := range service.GetMethod() {
				{
					fp, err := resolver.Resolve(method.GetInputType())
					if err == nil {
						if !samePackage(fp, file) {
							pfile.Imports[fp.GetName()] = &importValues{
								Name: importName(fp),
								Path: importPath(file, fp.GetName()),
							}
						}
					}
				}

				{
					fp, err := resolver.Resolve(method.GetOutputType())
					if err == nil {
						if !samePackage(fp, file) {
							pfile.Imports[fp.GetName()] = &importValues{
								Name: importName(fp),
								Path: importPath(file, fp.GetName()),
							}
						}
					}
				}

				v.Methods = append(v.Methods, &serviceMethodValues{
					Name:       method.GetName(),
					InputType:  resolver.TypeName(file, removePkg(method.GetInputType())),
					OutputType: resolver.TypeName(file, removePkg(method.GetOutputType())),
				})
			}

			pfile.Services = append(pfile.Services, v)
		}

		// Compile to typescript
		s, err := pfile.Compile()
		if err != nil {
			log.Fatal("could not compile template: ", err)
		}

		fileName := tsFileName(file.GetName())
		log.Printf("wrote: %v", fileName)

		res.File = append(res.File, &plugin.CodeGeneratorResponse_File{
			Name:    &fileName,
			Content: &s,
		})
	}

	return res, nil
}

func isRepeated(field *descriptor.FieldDescriptorProto) bool {
	return field.Label != nil && *field.Label == descriptor.FieldDescriptorProto_LABEL_REPEATED
}

func removePkg(s string) string {
	p := strings.Split(s, ".")
	return p[len(p)-1]
}

func upperCaseFirst(s string) string {
	return strings.ToUpper(s[0:1]) + s[1:]
}

func camelCase(s string) string {
	parts := strings.Split(s, "_")

	for i, p := range parts {
		if i == 0 {
			parts[i] = p
		} else {
			parts[i] = strings.ToUpper(p[0:1]) + p[1:]
		}
	}

	return strings.Join(parts, "")
}

func importName(fp *descriptor.FileDescriptorProto) string {
	return tsImportName(fp.GetName())
}

func tsImportName(name string) string {
	base := path.Base(name)
	return base[0 : len(base)-len(path.Ext(base))]
}

func tsImportPath(name string) string {
	base := path.Base(name)
	name = name[0 : len(name)-len(path.Ext(base))]
	return name
}

func importPath(fd *descriptor.FileDescriptorProto, name string) string {
	// TODO: how to resolve relative paths?
	return tsImportPath(name)
}

func tsFileName(name string) string {
	return tsImportPath(name) + ".ts"
}

func singularFieldType(f *descriptor.FieldDescriptorProto) string {
	switch f.GetType() {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_INT64:
		return "number"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		return "boolean"
	case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		name := f.GetTypeName()

		// Google WKT Timestamp is a special case here:
		//
		// Currently the value will just be left as jsonpb RFC 3339 string.
		// JSON.stringify already handles serializing Date to its RFC 3339 format.
		//
		if name == ".google.protobuf.Timestamp" {
			return "Date"
		}

		return removePkg(name)
	}

	return "string"
}

func fieldType(f *fieldValues) string {
	t := f.Type
	if t == "Date" {
		t = "string"
	}
	if f.IsRepeated {
		return t + "[]"
	}
	return t
}
