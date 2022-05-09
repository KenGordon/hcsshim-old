package oci

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/clone"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/pkg/annotations"
)

// handleCloneAnnotations handles parsing annotations related to template creation and cloning
// Since late cloning is only supported for WCOW this function only deals with WCOW options.
func handleCloneAnnotations(ctx context.Context, a map[string]string, wopts *uvm.OptionsWCOW) (err error) {
	wopts.IsTemplate = parseAnnotationsBool(ctx, a, annotations.SaveAsTemplate, false)
	templateID := parseAnnotationsString(a, annotations.TemplateID, "")
	if templateID != "" {
		tc, err := clone.FetchTemplateConfig(ctx, templateID)
		if err != nil {
			return err
		}
		wopts.TemplateConfig = &uvm.UVMTemplateConfig{
			UVMID:      tc.TemplateUVMID,
			CreateOpts: tc.TemplateUVMCreateOpts,
			Resources:  tc.TemplateUVMResources,
		}
		wopts.IsClone = true
	}
	return nil
}
