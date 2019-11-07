/*
Copyright 2019 the original author or authors.

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

package v1alpha1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/google/go-cmp/cmp"

	"github.com/projectriff/system/pkg/validation"
)

// +kubebuilder:webhook:path=/validate-streaming-projectriff-io-v1alpha1-processor,mutating=false,failurePolicy=fail,groups=streaming.projectriff.io,resources=processors,verbs=create;update,versions=v1alpha1,name=processors.streaming.projectriff.io

var (
	_ webhook.Validator         = &Processor{}
	_ validation.FieldValidator = &Processor{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *Processor) ValidateCreate() error {
	return r.Validate().ToAggregate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *Processor) ValidateUpdate(old runtime.Object) error {
	// TODO check for immutable fields
	return r.Validate().ToAggregate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *Processor) ValidateDelete() error {
	return nil
}

func (r *Processor) Validate() validation.FieldErrors {
	errs := validation.FieldErrors{}

	errs = errs.Also(r.Spec.Validate().ViaField("spec"))

	return errs
}

func (s *ProcessorSpec) Validate() validation.FieldErrors {
	if equality.Semantic.DeepEqual(s, &ProcessorSpec{}) {
		return validation.ErrMissingField(validation.CurrentField)
	}

	errs := validation.FieldErrors{}

	if diff := cmp.Diff(&corev1.PodSpec{
		// add supported PodSpec fields here, otherwise their usage will be rejected
		ServiceAccountName: s.Template.ServiceAccountName,
		// the defaulter guarantees at least one container
		Containers: filterInvalidContainers(s.Template.Containers[:1]),
		Volumes:    filterInvalidVolumes(s.Template.Volumes),
	}, s.Template); diff != "" {
		errs = errs.Also(validation.ErrDisallowedFields("template", fmt.Sprintf("limited Template fields may be set (-want, +got) = %v", diff)))
	}
	if s.Template.Containers[0].Name != "function" {
		errs = errs.Also(validation.ErrInvalidValue(s.Template.Containers[0].Name, "template.containers[0].name"))
	}

	if s.FunctionRef == "" && s.Template.Containers[0].Image == "" {
		errs = errs.Also(validation.ErrMissingOneOf("functionRef", "template.containers[0].image"))
	} else if s.FunctionRef != "" && s.Template.Containers[0].Image != "" {
		errs = errs.Also(validation.ErrMultipleOneOf("functionRef", "template.containers[0].image"))
	}

	// at least one input is required
	if len(s.Inputs) == 0 {
		errs = errs.Also(validation.ErrMissingField("inputs"))
	}
	for i, input := range s.Inputs {
		if input.Stream == "" {
			errs = errs.Also(validation.ErrMissingField("stream").ViaFieldIndex("inputs", i))
		}
		if input.Alias == "" {
			errs = errs.Also(validation.ErrMissingField("alias").ViaFieldIndex("inputs", i))
		}
	}

	// outputs are optional
	for i, output := range s.Outputs {
		if output.Stream == "" {
			errs = errs.Also(validation.ErrMissingField("stream").ViaFieldIndex("outputs", i))
		}
		if output.Alias == "" {
			errs = errs.Also(validation.ErrMissingField("alias").ViaFieldIndex("outputs", i))
		}
	}

	return errs
}

func filterInvalidContainers(containers []corev1.Container) []corev1.Container {
	// TODO remove unsupported fields
	return containers
}

func filterInvalidVolumes(volumes []corev1.Volume) []corev1.Volume {
	// TODO remove unsupported fields
	return volumes
}