/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handlers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

func TestHandlers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Handlers")
}

var _ = Describe("Generated network schemas", func() {
	nameOfLength := func(length int) string {
		var labels []string
		for length > 63 {
			labelLength := 63
			if length == 64 {
				labelLength = 62
			}
			labels = append(labels, strings.Repeat("a", labelLength))
			length -= labelLength + 1
		}
		return strings.Join(append(labels, strings.Repeat("a", length)), ".")
	}
	for _, resource := range []string{"replicatedvolumes", "drbdresources", "replicatedvolumereplicas"} {
		Context(resource+" downstream names", func() {
			It("accepts network names through 253 characters at every propagation endpoint", func() {
				// Generated CRDs are outside Go modules and cannot be embedded.
				data, err := os.ReadFile("../../../crds/newcontrolplane/storage.deckhouse.io_" + resource + ".yaml")
				Expect(err).NotTo(HaveOccurred())
				var crd apiextensionsv1.CustomResourceDefinition
				Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
				var schema apiextensions.JSONSchemaProps
				Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &schema, nil)).To(Succeed())
				paths := map[string][]string{
					"replicatedvolumes": {
						"status.datamesh.systemNetworkNames",
						"status.datamesh.members.[].addresses.[].systemNetworkName",
						"status.datameshTransitions.[].fromSystemNetworkNames",
						"status.datameshTransitions.[].toSystemNetworkNames",
					},
					"drbdresources": {
						"spec.systemNetworks",
						"spec.peers.[].paths.[].systemNetworkName",
						"status.addresses.[].systemNetworkName",
						"status.peers.[].paths.[].systemNetworkName",
					},
					"replicatedvolumereplicas": {
						"status.addresses.[].systemNetworkName",
						"status.peers.[].connectionEstablishedOn",
					},
				}
				for _, path := range paths[resource] {
					endpoint := schema
					for _, part := range strings.Split(path, ".") {
						if part == "[]" {
							Expect(endpoint.Items).NotTo(BeNil(), path)
							endpoint = *endpoint.Items.Schema
						} else {
							Expect(endpoint.Properties).To(HaveKey(part), path)
							endpoint = endpoint.Properties[part]
						}
					}
					openAPI, _, err := validation.NewSchemaValidator(&endpoint)
					Expect(err).NotTo(HaveOccurred())
					for _, length := range []int{64, 65, 253, 254} {
						// Keep every DNS label within 63 characters.
						name := nameOfLength(length)
						var value interface{} = name
						if endpoint.Type == "array" {
							value = []interface{}{name}
						}
						errs := validation.ValidateCustomResource(field.NewPath(path), value, openAPI)
						Expect(len(errs) == 0).To(Equal(length <= 253), fmt.Sprintf("%s length %d: %v", path, length, errs))
					}
				}
			})
		})
	}
	for _, resource := range []string{"replicatedstorageclasses", "replicatedstoragepools"} {
		Context(resource, func() {
			var specSchema apiextensions.JSONSchemaProps
			var structural *structuralschema.Structural
			BeforeEach(func() {
				// Generated CRDs are outside Go modules and cannot be embedded.
				data, err := os.ReadFile("../../../crds/storage.deckhouse.io_" + resource + ".yaml")
				Expect(err).NotTo(HaveOccurred())
				var crd apiextensionsv1.CustomResourceDefinition
				Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
				var schema apiextensions.JSONSchemaProps
				Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &schema, nil)).To(Succeed())
				specSchema = schema.Properties["spec"]
				structural, err = structuralschema.NewStructural(&specSchema)
				Expect(err).NotTo(HaveOccurred())
			})

			It("retains cardinality, item length, list semantics and default semantics", func() {
				network := specSchema.Properties["systemNetworkNames"]
				Expect(network.MinItems).To(HaveValue(Equal(int64(1))))
				Expect(network.MaxItems).To(HaveValue(Equal(int64(1))))
				Expect(network.Items.Schema.MaxLength).To(HaveValue(Equal(int64(253))))
				obj := map[string]interface{}{}
				defaulting.Default(obj, structural)
				if resource == "replicatedstorageclasses" {
					Expect(network.XListType).To(HaveValue(Equal("set")))
					Expect(specSchema.Required).NotTo(ContainElement("systemNetworkNames"))
					Expect(obj).NotTo(HaveKey("systemNetworkNames"))
				} else {
					Expect(network.XListType).To(HaveValue(Equal("atomic")))
					Expect(obj["systemNetworkNames"]).To(Equal([]interface{}{"Internal"}))
				}
			})

			DescribeTable("validates names with both OpenAPI and CEL", func(names []interface{}, valid bool) {
				networkSchema := specSchema.Properties["systemNetworkNames"]
				openAPI, _, err := validation.NewSchemaValidator(&networkSchema)
				Expect(err).NotTo(HaveOccurred())
				errs := validation.ValidateCustomResource(field.NewPath("spec", "systemNetworkNames"), names, openAPI)
				networkStructural := structural.Properties["systemNetworkNames"]
				cel := schemacel.NewValidator(&networkStructural, false, celconfig.PerCallLimit)
				Expect(cel).NotTo(BeNil())
				celErrs, _ := cel.Validate(context.Background(), field.NewPath("spec", "systemNetworkNames"), &networkStructural, names, nil, celconfig.RuntimeCELCostBudget)
				errs = append(errs, celErrs...)
				Expect(len(errs) == 0).To(Equal(valid), "%v", errs)
			},
				Entry("Internal", []interface{}{"Internal"}, true),
				Entry("DNS subdomain", []interface{}{"replication.net-1"}, true),
				Entry("64 characters", []interface{}{nameOfLength(64)}, true),
				Entry("65 characters", []interface{}{nameOfLength(65)}, true),
				Entry("253 characters", []interface{}{nameOfLength(253)}, true),
				Entry("254 characters", []interface{}{nameOfLength(254)}, false),
				Entry("empty name", []interface{}{""}, false),
				Entry("uppercase", []interface{}{"Network"}, false),
				Entry("underscore", []interface{}{"net_work"}, false),
				Entry("leading hyphen", []interface{}{"-net"}, false),
				Entry("trailing hyphen", []interface{}{"net-"}, false),
				Entry("empty label", []interface{}{"net..work"}, false),
				Entry("leading dot", []interface{}{".net"}, false),
				Entry("trailing dot", []interface{}{"net."}, false),
				Entry("hyphen beside dot", []interface{}{"net-.work"}, false),
				Entry("no networks", []interface{}{}, false),
				Entry("two networks", []interface{}{"Internal", "net"}, false),
			)

			DescribeTable("keeps networks temporarily immutable without relaxing other transition rules", func(oldName, newName string, valid bool) {
				if resource != "replicatedstorageclasses" && (oldName == "" || newName == "") {
					Skip("Omission transitions apply only to optional RSC networks")
				}
				oldObj := map[string]interface{}{"systemNetworkNames": []interface{}{oldName}}
				newObj := map[string]interface{}{"systemNetworkNames": []interface{}{newName}}
				if oldName == "" {
					delete(oldObj, "systemNetworkNames")
				}
				if newName == "" {
					delete(newObj, "systemNetworkNames")
				}
				if resource == "replicatedstorageclasses" {
					oldObj["topology"], newObj["topology"] = "Ignored", "Ignored"
					oldObj["reclaimPolicy"], newObj["reclaimPolicy"] = "Delete", "Delete"
				} else {
					oldObj["type"], newObj["type"] = "LVM", "LVM"
					oldObj["lvmVolumeGroups"] = []interface{}{map[string]interface{}{"name": "vg"}}
					newObj["lvmVolumeGroups"] = oldObj["lvmVolumeGroups"]
				}
				cel := schemacel.NewValidator(structural, false, celconfig.PerCallLimit)
				Expect(cel).NotTo(BeNil())
				errs, _ := cel.Validate(context.Background(), field.NewPath("spec"), structural, newObj, oldObj, celconfig.RuntimeCELCostBudget)
				Expect(len(errs) == 0).To(Equal(valid), "%v", errs)
				if resource == "replicatedstorageclasses" {
					newObj["topology"] = "Zonal"
				} else {
					newObj["lvmVolumeGroups"] = []interface{}{map[string]interface{}{"name": "other-vg"}}
				}
				errs, _ = cel.Validate(context.Background(), field.NewPath("spec"), structural, newObj, oldObj, celconfig.RuntimeCELCostBudget)
				Expect(errs).NotTo(BeEmpty())
			},
				Entry("unchanged Internal", "Internal", "Internal", true),
				Entry("unchanged SDN", "replication.net", "replication.net", true),
				Entry("Internal to SDN", "Internal", "replication.net", false),
				Entry("SDN to Internal", "replication.net", "Internal", false),
				Entry("SDN to SDN", "replication.net", "other.net", false),
				Entry("Internal removal", "Internal", "", false),
				Entry("SDN removal", "replication.net", "", false),
				Entry("initial controller default", "", "Internal", true),
				Entry("unchanged omission", "", "", true),
			)
		})
	}
})
