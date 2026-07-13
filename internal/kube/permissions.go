package kube

import (
	"context"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type permission struct {
	group, resource, subresource, verb string
}

var readPermissions = []permission{
	{resource: "nodes", verb: "get"},
	{resource: "nodes", verb: "list"},
	{resource: "pods", verb: "list"},
	{resource: "persistentvolumes", verb: "list"},
	{resource: "persistentvolumeclaims", verb: "list"},
	{group: "policy", resource: "poddisruptionbudgets", verb: "list"},
	{group: "kubevirt.io", resource: "virtualmachineinstances", verb: "list"},
	{group: "kubevirt.io", resource: "virtualmachineinstances", verb: "get"},
	{group: "kubevirt.io", resource: "virtualmachineinstancemigrations", verb: "list"},
	{group: "kubevirt.io", resource: "virtualmachines", verb: "get"},
	{group: "kubevirt.io", resource: "kubevirts", verb: "list"},
}

func (c Clients) CheckStatusPermissions(ctx context.Context) error {
	return c.checkPermissions(ctx, readPermissions)
}

func (c Clients) CheckDrainPermissions(ctx context.Context) error {
	permissions := append([]permission{}, readPermissions...)
	permissions = append(permissions,
		permission{resource: "nodes", verb: "patch"},
		permission{resource: "pods", subresource: "eviction", verb: "create"},
		permission{resource: "events", verb: "list"},
	)
	return c.checkPermissions(ctx, permissions)
}

func (c Clients) CheckWatchPermissions(ctx context.Context) error {
	return c.checkPermissions(ctx, []permission{
		{group: "kubevirt.io", resource: "virtualmachineinstancemigrations", verb: "list"},
		{group: "kubevirt.io", resource: "virtualmachineinstancemigrations", verb: "watch"},
	})
}

func (c Clients) CheckUncordonPermissions(ctx context.Context) error {
	return c.checkPermissions(ctx, []permission{{resource: "nodes", verb: "patch"}})
}

func (c Clients) checkPermissions(ctx context.Context, permissions []permission) error {
	var denied []string
	for _, item := range permissions {
		review, err := c.Core.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group: item.group, Resource: item.resource, Subresource: item.subresource, Verb: item.verb,
			}}}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("check %s permission for %s: %w", item.verb, item.resource, err)
		}
		if review.Status.Allowed {
			continue
		}
		resource := item.resource
		if item.subresource != "" {
			resource += "/" + item.subresource
		}
		reason := strings.TrimSpace(review.Status.Reason)
		if reason != "" {
			denied = append(denied, fmt.Sprintf("%s %s (%s)", item.verb, resource, reason))
		} else {
			denied = append(denied, item.verb+" "+resource)
		}
	}
	if len(denied) > 0 {
		return fmt.Errorf("missing permissions: %s", strings.Join(denied, ", "))
	}
	return nil
}
