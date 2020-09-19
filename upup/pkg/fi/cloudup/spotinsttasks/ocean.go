/*
Copyright 2019 The Kubernetes Authors.

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

package spotinsttasks

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	"github.com/spotinst/spotinst-sdk-go/spotinst/client"
	"github.com/spotinst/spotinst-sdk-go/spotinst/util/stringutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/resources/spotinst"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup/awstasks"
	"k8s.io/kops/upup/pkg/fi/cloudup/awsup"
	"k8s.io/kops/upup/pkg/fi/cloudup/terraform"
)

// +kops:fitask
type Ocean struct {
	Name      *string
	Lifecycle *fi.Lifecycle

	ID                       *string
	MinSize                  *int64
	MaxSize                  *int64
	SpotPercentage           *float64
	UtilizeReservedInstances *bool
	FallbackToOnDemand       *bool
	DrainingTimeout          *int64
	GracePeriod              *int64
	InstanceTypesWhitelist   []string
	InstanceTypesBlacklist   []string
	Tags                     map[string]string
	UserData                 *fi.ResourceHolder
	ImageID                  *string
	IAMInstanceProfile       *awstasks.IAMInstanceProfile
	SSHKey                   *awstasks.SSHKey
	Subnets                  []*awstasks.Subnet
	SecurityGroups           []*awstasks.SecurityGroup
	Monitoring               *bool
	AssociatePublicIP        *bool
	RootVolumeOpts           *RootVolumeOpts
	AutoScalerOpts           *AutoScalerOpts
}

var _ fi.Task = &Ocean{}
var _ fi.CompareWithID = &Ocean{}
var _ fi.HasDependencies = &Ocean{}

func (o *Ocean) CompareWithID() *string {
	return o.Name
}

func (o *Ocean) GetDependencies(tasks map[string]fi.Task) []fi.Task {
	var deps []fi.Task

	if o.IAMInstanceProfile != nil {
		deps = append(deps, o.IAMInstanceProfile)
	}

	if o.SSHKey != nil {
		deps = append(deps, o.SSHKey)
	}

	if o.Subnets != nil {
		for _, subnet := range o.Subnets {
			deps = append(deps, subnet)
		}
	}

	if o.SecurityGroups != nil {
		for _, sg := range o.SecurityGroups {
			deps = append(deps, sg)
		}
	}

	if o.UserData != nil {
		deps = append(deps, o.UserData.GetDependencies(tasks)...)
	}

	return deps
}

func (o *Ocean) find(svc spotinst.InstanceGroupService, name string) (*aws.Cluster, error) {
	klog.V(4).Infof("Attempting to find Ocean: %q", name)

	oceans, err := svc.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("spotinst: failed to find ocean %q: %v", name, err)
	}

	var out *aws.Cluster
	for _, ocean := range oceans {
		if ocean.Name() == name {
			out = ocean.Obj().(*aws.Cluster)
			break
		}
	}
	if out == nil {
		return nil, fmt.Errorf("spotinst: failed to find ocean %q", name)
	}

	klog.V(4).Infof("Ocean/%s: %s", name, stringutil.Stringify(out))
	return out, nil
}

var _ fi.HasCheckExisting = &Ocean{}

func (o *Ocean) Find(c *fi.Context) (*Ocean, error) {
	cloud := c.Cloud.(awsup.AWSCloud)

	ocean, err := o.find(cloud.Spotinst().Ocean(), *o.Name)
	if err != nil {
		return nil, err
	}

	actual := &Ocean{}
	actual.ID = ocean.ID
	actual.Name = ocean.Name

	// Capacity.
	{
		actual.MinSize = fi.Int64(int64(fi.IntValue(ocean.Capacity.Minimum)))
		actual.MaxSize = fi.Int64(int64(fi.IntValue(ocean.Capacity.Maximum)))
	}

	// Strategy.
	{
		if strategy := ocean.Strategy; strategy != nil {
			actual.SpotPercentage = strategy.SpotPercentage
			actual.FallbackToOnDemand = strategy.FallbackToOnDemand
			actual.UtilizeReservedInstances = strategy.UtilizeReservedInstances

			if strategy.DrainingTimeout != nil {
				actual.DrainingTimeout = fi.Int64(int64(fi.IntValue(strategy.DrainingTimeout)))
			}

			if strategy.GracePeriod != nil {
				actual.GracePeriod = fi.Int64(int64(fi.IntValue(strategy.GracePeriod)))
			}
		}
	}

	// Compute.
	{
		compute := ocean.Compute

		// Subnets.
		{
			if subnets := compute.SubnetIDs; subnets != nil {
				for _, subnetID := range subnets {
					actual.Subnets = append(actual.Subnets,
						&awstasks.Subnet{ID: fi.String(subnetID)})
				}
				if subnetSlicesEqualIgnoreOrder(actual.Subnets, o.Subnets) {
					actual.Subnets = o.Subnets
				}
			}
		}

		// Instance types.
		{
			if itypes := compute.InstanceTypes; itypes != nil {
				// Whitelist.
				if len(itypes.Whitelist) > 0 {
					actual.InstanceTypesWhitelist = itypes.Whitelist
				}

				// Blacklist.
				if len(itypes.Blacklist) > 0 {
					actual.InstanceTypesBlacklist = itypes.Blacklist
				}
			}
		}
	}

	// Launch specification.
	{
		lc := ocean.Compute.LaunchSpecification

		// Image.
		{
			actual.ImageID = lc.ImageID

			if o.ImageID != nil && actual.ImageID != nil &&
				fi.StringValue(actual.ImageID) != fi.StringValue(o.ImageID) {
				image, err := resolveImage(cloud, fi.StringValue(o.ImageID))
				if err != nil {
					return nil, err
				}
				if fi.StringValue(image.ImageId) == fi.StringValue(lc.ImageID) {
					actual.ImageID = o.ImageID
				}
			}
		}

		// Tags.
		{
			if lc.Tags != nil && len(lc.Tags) > 0 {
				actual.Tags = make(map[string]string)
				for _, tag := range lc.Tags {
					actual.Tags[fi.StringValue(tag.Key)] = fi.StringValue(tag.Value)
				}
			}
		}

		// Security groups.
		{
			if lc.SecurityGroupIDs != nil {
				for _, sgID := range lc.SecurityGroupIDs {
					actual.SecurityGroups = append(actual.SecurityGroups,
						&awstasks.SecurityGroup{ID: fi.String(sgID)})
				}
			}
		}

		// User data.
		{
			var userData []byte

			if lc.UserData != nil {
				userData, err = base64.StdEncoding.DecodeString(fi.StringValue(lc.UserData))
				if err != nil {
					return nil, err
				}
			}

			actual.UserData = fi.WrapResource(fi.NewStringResource(string(userData)))
		}

		// EBS optimization.
		{
			if fi.BoolValue(lc.EBSOptimized) {
				if actual.RootVolumeOpts == nil {
					actual.RootVolumeOpts = new(RootVolumeOpts)
				}

				actual.RootVolumeOpts.Optimization = lc.EBSOptimized
			}
		}

		// IAM instance profile.
		if lc.IAMInstanceProfile != nil {
			actual.IAMInstanceProfile = &awstasks.IAMInstanceProfile{Name: lc.IAMInstanceProfile.Name}
		}

		// SSH key.
		if lc.KeyPair != nil {
			actual.SSHKey = &awstasks.SSHKey{Name: lc.KeyPair}
		}

		// Public IP.
		if lc.AssociatePublicIPAddress != nil {
			actual.AssociatePublicIP = lc.AssociatePublicIPAddress
		}

		// Root volume options.
		if lc.RootVolumeSize != nil {
			actual.RootVolumeOpts = new(RootVolumeOpts)
			actual.RootVolumeOpts.Size = fi.Int32(int32(*lc.RootVolumeSize))
		}

		// Monitoring.
		if lc.Monitoring != nil {
			actual.Monitoring = lc.Monitoring
		}
	}

	// Auto Scaler.
	{
		if ocean.AutoScaler != nil {
			actual.AutoScalerOpts = new(AutoScalerOpts)
			actual.AutoScalerOpts.ClusterID = ocean.ControllerClusterID
			actual.AutoScalerOpts.Enabled = ocean.AutoScaler.IsEnabled
			actual.AutoScalerOpts.Cooldown = ocean.AutoScaler.Cooldown

			// Headroom.
			if headroom := ocean.AutoScaler.Headroom; headroom != nil {
				actual.AutoScalerOpts.Headroom = &AutoScalerHeadroomOpts{
					CPUPerUnit: headroom.CPUPerUnit,
					GPUPerUnit: headroom.GPUPerUnit,
					MemPerUnit: headroom.MemoryPerUnit,
					NumOfUnits: headroom.NumOfUnits,
				}
			}

			// Scale down.
			if down := ocean.AutoScaler.Down; down != nil {
				actual.AutoScalerOpts.Down = &AutoScalerDownOpts{
					MaxPercentage:     down.MaxScaleDownPercentage,
					EvaluationPeriods: down.EvaluationPeriods,
				}
			}
		}
	}

	// Avoid spurious changes.
	actual.Lifecycle = o.Lifecycle

	return actual, nil
}

func (o *Ocean) CheckExisting(c *fi.Context) bool {
	cloud := c.Cloud.(awsup.AWSCloud)
	ocean, err := o.find(cloud.Spotinst().Ocean(), *o.Name)
	return err == nil && ocean != nil
}

func (o *Ocean) Run(c *fi.Context) error {
	return fi.DefaultDeltaRunMethod(o, c)
}

func (s *Ocean) CheckChanges(a, e, changes *Ocean) error {
	if e.Name == nil {
		return fi.RequiredField("Name")
	}
	return nil
}

func (o *Ocean) RenderAWS(t *awsup.AWSAPITarget, a, e, changes *Ocean) error {
	return o.createOrUpdate(t.Cloud.(awsup.AWSCloud), a, e, changes)
}

func (o *Ocean) createOrUpdate(cloud awsup.AWSCloud, a, e, changes *Ocean) error {
	if a == nil {
		return o.create(cloud, a, e, changes)
	} else {
		return o.update(cloud, a, e, changes)
	}
}

func (_ *Ocean) create(cloud awsup.AWSCloud, a, e, changes *Ocean) error {
	klog.V(2).Infof("Creating Ocean %q", *e.Name)
	e.applyDefaults()

	ocean := &aws.Cluster{
		Capacity: new(aws.Capacity),
		Strategy: new(aws.Strategy),
		Compute: &aws.Compute{
			LaunchSpecification: new(aws.LaunchSpecification),
		},
	}

	// General.
	{
		ocean.SetName(e.Name)
		ocean.SetRegion(fi.String(cloud.Region()))
	}

	// Capacity.
	{
		ocean.Capacity.SetTarget(fi.Int(int(*e.MinSize)))
		ocean.Capacity.SetMinimum(fi.Int(int(*e.MinSize)))
		ocean.Capacity.SetMaximum(fi.Int(int(*e.MaxSize)))
	}

	// Strategy.
	{
		ocean.Strategy.SetSpotPercentage(e.SpotPercentage)
		ocean.Strategy.SetFallbackToOnDemand(e.FallbackToOnDemand)
		ocean.Strategy.SetUtilizeReservedInstances(e.UtilizeReservedInstances)

		if e.DrainingTimeout != nil {
			ocean.Strategy.SetDrainingTimeout(fi.Int(int(*e.DrainingTimeout)))
		}

		if e.GracePeriod != nil {
			ocean.Strategy.SetGracePeriod(fi.Int(int(*e.GracePeriod)))
		}
	}

	// Compute.
	{
		// Subnets.
		{
			if e.Subnets != nil {
				subnetIDs := make([]string, len(e.Subnets))
				for i, subnet := range e.Subnets {
					subnetIDs[i] = fi.StringValue(subnet.ID)
				}
				ocean.Compute.SetSubnetIDs(subnetIDs)
			}
		}

		// Instance types.
		{
			itypes := new(aws.InstanceTypes)

			// Whitelist.
			if e.InstanceTypesWhitelist != nil {
				itypes.SetWhitelist(e.InstanceTypesWhitelist)
			}

			// Blacklist.
			if e.InstanceTypesBlacklist != nil {
				itypes.SetBlacklist(e.InstanceTypesBlacklist)
			}

			if len(itypes.Whitelist) > 0 || len(itypes.Blacklist) > 0 {
				ocean.Compute.SetInstanceTypes(itypes)
			}
		}

		// Launch specification.
		{
			ocean.Compute.LaunchSpecification.SetMonitoring(e.Monitoring)
			ocean.Compute.LaunchSpecification.SetKeyPair(e.SSHKey.Name)

			// Image.
			{
				if e.ImageID != nil {
					image, err := resolveImage(cloud, fi.StringValue(e.ImageID))
					if err != nil {
						return err
					}
					ocean.Compute.LaunchSpecification.SetImageId(image.ImageId)
				}
			}

			// User data.
			{
				if e.UserData != nil {
					userData, err := e.UserData.AsString()
					if err != nil {
						return err
					}

					if len(userData) > 0 {
						encoded := base64.StdEncoding.EncodeToString([]byte(userData))
						ocean.Compute.LaunchSpecification.SetUserData(fi.String(encoded))
					}
				}
			}

			// IAM instance profile.
			{
				if e.IAMInstanceProfile != nil {
					iprof := new(aws.IAMInstanceProfile)
					iprof.SetName(e.IAMInstanceProfile.GetName())
					ocean.Compute.LaunchSpecification.SetIAMInstanceProfile(iprof)
				}
			}

			// Security groups.
			{
				if e.SecurityGroups != nil {
					securityGroupIDs := make([]string, len(e.SecurityGroups))
					for i, sg := range e.SecurityGroups {
						securityGroupIDs[i] = *sg.ID
					}
					ocean.Compute.LaunchSpecification.SetSecurityGroupIDs(securityGroupIDs)
				}
			}

			// Public IP.
			{
				if e.AssociatePublicIP != nil {
					ocean.Compute.LaunchSpecification.SetAssociatePublicIPAddress(e.AssociatePublicIP)
				}
			}

			// Root volume options.
			{
				if opts := e.RootVolumeOpts; opts != nil {

					// Volume size.
					if opts.Size != nil {
						ocean.Compute.LaunchSpecification.SetRootVolumeSize(fi.Int(int(*opts.Size)))
					}

					// EBS optimization.
					if opts.Optimization != nil {
						ocean.Compute.LaunchSpecification.SetEBSOptimized(opts.Optimization)
					}
				}
			}

			// Tags.
			{
				if e.Tags != nil {
					ocean.Compute.LaunchSpecification.SetTags(e.buildTags())
				}
			}
		}
	}

	// Auto Scaler.
	{
		if opts := e.AutoScalerOpts; opts != nil {
			ocean.SetControllerClusterId(opts.ClusterID)

			if opts.Enabled != nil {
				autoScaler := new(aws.AutoScaler)
				autoScaler.IsEnabled = opts.Enabled
				autoScaler.IsAutoConfig = fi.Bool(true)
				autoScaler.Cooldown = opts.Cooldown

				// Headroom.
				if headroom := opts.Headroom; headroom != nil {
					autoScaler.IsAutoConfig = fi.Bool(false)
					autoScaler.Headroom = &aws.AutoScalerHeadroom{
						CPUPerUnit:    headroom.CPUPerUnit,
						GPUPerUnit:    headroom.GPUPerUnit,
						MemoryPerUnit: headroom.MemPerUnit,
						NumOfUnits:    headroom.NumOfUnits,
					}
				}

				// Scale down.
				if down := opts.Down; down != nil {
					autoScaler.Down = &aws.AutoScalerDown{
						MaxScaleDownPercentage: down.MaxPercentage,
						EvaluationPeriods:      down.EvaluationPeriods,
					}
				}

				ocean.SetAutoScaler(autoScaler)
			}
		}
	}

	attempt := 0
	maxAttempts := 10

readyLoop:
	for {
		attempt++
		klog.V(2).Infof("(%d/%d) Attempting to create Ocean: %q, config: %s",
			attempt, maxAttempts, *e.Name, stringutil.Stringify(ocean))

		// Wait for IAM instance profile to be ready.
		time.Sleep(10 * time.Second)

		// Wrap the raw object as an Ocean.
		oc, err := spotinst.NewOcean(cloud.ProviderID(), ocean)
		if err != nil {
			return err
		}

		// Create a new Ocean.
		id, err := cloud.Spotinst().Ocean().Create(context.Background(), oc)
		if err == nil {
			e.ID = fi.String(id)
			break
		}

		if errs, ok := err.(client.Errors); ok {
			for _, err := range errs {
				if strings.Contains(err.Message, "Invalid IAM Instance Profile name") {
					if attempt > maxAttempts {
						return fmt.Errorf("IAM instance profile not yet created/propagated (original error: %v)", err)
					}

					klog.V(4).Infof("Got an error indicating that the IAM instance profile %q is not ready %q", fi.StringValue(e.IAMInstanceProfile.Name), err)
					klog.Infof("Waiting for IAM instance profile %q to be ready", fi.StringValue(e.IAMInstanceProfile.Name))
					goto readyLoop
				}
			}

			return fmt.Errorf("spotinst: failed to create ocean: %v", err)
		}
	}

	return nil
}

func (_ *Ocean) update(cloud awsup.AWSCloud, a, e, changes *Ocean) error {
	klog.V(2).Infof("Updating Ocean %q", *e.Name)

	actual, err := e.find(cloud.Spotinst().Ocean(), *e.Name)
	if err != nil {
		klog.Errorf("Unable to resolve Ocean %q, error: %s", *e.Name, err)
		return err
	}

	var changed bool
	ocean := new(aws.Cluster)
	ocean.SetId(actual.ID)

	// Strategy.
	{
		// Spot percentage.
		if changes.SpotPercentage != nil {
			if ocean.Strategy == nil {
				ocean.Strategy = new(aws.Strategy)
			}

			ocean.Strategy.SetSpotPercentage(e.SpotPercentage)
			changes.SpotPercentage = nil
			changed = true
		}

		// Fallback to on-demand.
		if changes.FallbackToOnDemand != nil {
			if ocean.Strategy == nil {
				ocean.Strategy = new(aws.Strategy)
			}

			ocean.Strategy.SetFallbackToOnDemand(e.FallbackToOnDemand)
			changes.FallbackToOnDemand = nil
			changed = true
		}

		// Utilize reserved instances.
		if changes.UtilizeReservedInstances != nil {
			if ocean.Strategy == nil {
				ocean.Strategy = new(aws.Strategy)
			}

			ocean.Strategy.SetUtilizeReservedInstances(e.UtilizeReservedInstances)
			changes.UtilizeReservedInstances = nil
			changed = true
		}

		// Draining timeout.
		if changes.DrainingTimeout != nil {
			if ocean.Strategy == nil {
				ocean.Strategy = new(aws.Strategy)
			}

			ocean.Strategy.SetDrainingTimeout(fi.Int(int(*e.DrainingTimeout)))
			changes.DrainingTimeout = nil
			changed = true
		}

		// Grace period.
		if changes.GracePeriod != nil {
			if ocean.Strategy == nil {
				ocean.Strategy = new(aws.Strategy)
			}

			ocean.Strategy.SetGracePeriod(fi.Int(int(*e.GracePeriod)))
			changes.GracePeriod = nil
			changed = true
		}
	}

	// Compute.
	{
		// Subnets.
		{
			if changes.Subnets != nil {
				if ocean.Compute == nil {
					ocean.Compute = new(aws.Compute)
				}

				subnetIDs := make([]string, len(e.Subnets))
				for i, subnet := range e.Subnets {
					subnetIDs[i] = fi.StringValue(subnet.ID)
				}

				ocean.Compute.SetSubnetIDs(subnetIDs)
				changes.Subnets = nil
				changed = true
			}
		}

		// Instance types.
		{
			// Whitelist.
			{
				if changes.InstanceTypesWhitelist != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.InstanceTypes == nil {
						ocean.Compute.InstanceTypes = new(aws.InstanceTypes)
					}

					ocean.Compute.InstanceTypes.SetWhitelist(e.InstanceTypesWhitelist)
					changes.InstanceTypesWhitelist = nil
					changed = true
				}
			}

			// Blacklist.
			{
				if changes.InstanceTypesBlacklist != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.InstanceTypes == nil {
						ocean.Compute.InstanceTypes = new(aws.InstanceTypes)
					}

					ocean.Compute.InstanceTypes.SetBlacklist(e.InstanceTypesBlacklist)
					changes.InstanceTypesBlacklist = nil
					changed = true
				}
			}
		}

		// Launch specification.
		{
			// Security groups.
			{
				if changes.SecurityGroups != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					securityGroupIDs := make([]string, len(e.SecurityGroups))
					for i, sg := range e.SecurityGroups {
						securityGroupIDs[i] = *sg.ID
					}

					ocean.Compute.LaunchSpecification.SetSecurityGroupIDs(securityGroupIDs)
					changes.SecurityGroups = nil
					changed = true
				}
			}

			// User data.
			{
				if changes.UserData != nil {
					userData, err := e.UserData.AsString()
					if err != nil {
						return err
					}

					if len(userData) > 0 {
						if ocean.Compute == nil {
							ocean.Compute = new(aws.Compute)
						}
						if ocean.Compute.LaunchSpecification == nil {
							ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
						}

						encoded := base64.StdEncoding.EncodeToString([]byte(userData))
						ocean.Compute.LaunchSpecification.SetUserData(fi.String(encoded))
						changed = true
					}

					changes.UserData = nil
				}
			}

			// Image.
			{
				if changes.ImageID != nil {
					image, err := resolveImage(cloud, fi.StringValue(e.ImageID))
					if err != nil {
						return err
					}

					if *actual.Compute.LaunchSpecification.ImageID != *image.ImageId {
						if ocean.Compute == nil {
							ocean.Compute = new(aws.Compute)
						}
						if ocean.Compute.LaunchSpecification == nil {
							ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
						}

						ocean.Compute.LaunchSpecification.SetImageId(image.ImageId)
						changed = true
					}

					changes.ImageID = nil
				}
			}

			// Tags.
			{
				if changes.Tags != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					ocean.Compute.LaunchSpecification.SetTags(e.buildTags())
					changes.Tags = nil
					changed = true
				}
			}

			// IAM instance profile.
			{
				if changes.IAMInstanceProfile != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					iprof := new(aws.IAMInstanceProfile)
					iprof.SetName(e.IAMInstanceProfile.GetName())

					ocean.Compute.LaunchSpecification.SetIAMInstanceProfile(iprof)
					changes.IAMInstanceProfile = nil
					changed = true
				}
			}

			// Monitoring.
			{
				if changes.Monitoring != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					ocean.Compute.LaunchSpecification.SetMonitoring(e.Monitoring)
					changes.Monitoring = nil
					changed = true
				}
			}

			// SSH key.
			{
				if changes.SSHKey != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					ocean.Compute.LaunchSpecification.SetKeyPair(e.SSHKey.Name)
					changes.SSHKey = nil
					changed = true
				}
			}

			// Public IP.
			{
				if changes.AssociatePublicIP != nil {
					if ocean.Compute == nil {
						ocean.Compute = new(aws.Compute)
					}
					if ocean.Compute.LaunchSpecification == nil {
						ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
					}

					ocean.Compute.LaunchSpecification.SetAssociatePublicIPAddress(e.AssociatePublicIP)
					changes.AssociatePublicIP = nil
					changed = true
				}
			}

			// Root volume options.
			{
				if opts := changes.RootVolumeOpts; opts != nil {

					// Volume size.
					if opts.Size != nil {
						if ocean.Compute == nil {
							ocean.Compute = new(aws.Compute)
						}
						if ocean.Compute.LaunchSpecification == nil {
							ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
						}

						ocean.Compute.LaunchSpecification.SetRootVolumeSize(fi.Int(int(*opts.Size)))
						changed = true
					}

					// EBS optimization.
					if opts.Optimization != nil {
						if ocean.Compute == nil {
							ocean.Compute = new(aws.Compute)
						}
						if ocean.Compute.LaunchSpecification == nil {
							ocean.Compute.LaunchSpecification = new(aws.LaunchSpecification)
						}

						ocean.Compute.LaunchSpecification.SetEBSOptimized(e.RootVolumeOpts.Optimization)
						changed = true
					}

					changes.RootVolumeOpts = nil
				}
			}
		}
	}

	// Capacity.
	{
		if changes.MinSize != nil {
			if ocean.Capacity == nil {
				ocean.Capacity = new(aws.Capacity)
			}

			ocean.Capacity.SetMinimum(fi.Int(int(*e.MinSize)))
			changes.MinSize = nil
			changed = true

			// Scale up the target capacity, if needed.
			if int64(*actual.Capacity.Target) < *e.MinSize {
				ocean.Capacity.SetTarget(fi.Int(int(*e.MinSize)))
			}
		}
		if changes.MaxSize != nil {
			if ocean.Capacity == nil {
				ocean.Capacity = new(aws.Capacity)
			}

			ocean.Capacity.SetMaximum(fi.Int(int(*e.MaxSize)))
			changes.MaxSize = nil
			changed = true
		}
	}

	// Auto Scaler.
	{
		if opts := changes.AutoScalerOpts; opts != nil {
			if opts.Enabled != nil {
				autoScaler := new(aws.AutoScaler)
				autoScaler.IsEnabled = e.AutoScalerOpts.Enabled
				autoScaler.Cooldown = e.AutoScalerOpts.Cooldown

				// Headroom.
				if headroom := opts.Headroom; headroom != nil {
					autoScaler.IsAutoConfig = fi.Bool(false)
					autoScaler.Headroom = &aws.AutoScalerHeadroom{
						CPUPerUnit:    e.AutoScalerOpts.Headroom.CPUPerUnit,
						GPUPerUnit:    e.AutoScalerOpts.Headroom.GPUPerUnit,
						MemoryPerUnit: e.AutoScalerOpts.Headroom.MemPerUnit,
						NumOfUnits:    e.AutoScalerOpts.Headroom.NumOfUnits,
					}
				} else if a.AutoScalerOpts != nil && a.AutoScalerOpts.Headroom != nil {
					autoScaler.IsAutoConfig = fi.Bool(true)
					autoScaler.SetHeadroom(nil)
				}

				// Scale down.
				if down := opts.Down; down != nil {
					autoScaler.Down = &aws.AutoScalerDown{
						MaxScaleDownPercentage: down.MaxPercentage,
						EvaluationPeriods:      down.EvaluationPeriods,
					}
				} else if a.AutoScalerOpts.Down != nil {
					autoScaler.SetDown(nil)
				}

				ocean.SetAutoScaler(autoScaler)
				changed = true
			}

			changes.AutoScalerOpts = nil
		}
	}

	empty := &Ocean{}
	if !reflect.DeepEqual(empty, changes) {
		klog.Warningf("Not all changes applied to Ocean %q: %v", *ocean.ID, changes)
	}

	if !changed {
		klog.V(2).Infof("No changes detected in Ocean %q", *ocean.ID)
		return nil
	}

	klog.V(2).Infof("Updating Ocean %q (config: %s)", *ocean.ID, stringutil.Stringify(ocean))

	// Wrap the raw object as an Ocean.
	oc, err := spotinst.NewOcean(cloud.ProviderID(), ocean)
	if err != nil {
		return err
	}

	// Update an existing Ocean.
	if err := cloud.Spotinst().Ocean().Update(context.Background(), oc); err != nil {
		return fmt.Errorf("spotinst: failed to update ocean: %v", err)
	}

	return nil
}

type terraformOcean struct {
	Name                   *string              `json:"name,omitempty" cty:"name"`
	ControllerClusterID    *string              `json:"controller_id,omitempty" cty:"controller_id"`
	Region                 *string              `json:"region,omitempty" cty:"region"`
	InstanceTypesWhitelist []string             `json:"whitelist,omitempty" cty:"whitelist"`
	InstanceTypesBlacklist []string             `json:"blacklist,omitempty" cty:"blacklist"`
	SubnetIDs              []*terraform.Literal `json:"subnet_ids,omitempty" cty:"subnet_ids"`
	AutoScaler             *terraformAutoScaler `json:"autoscaler,omitempty" cty:"autoscaler"`
	Tags                   []*terraformKV       `json:"tags,omitempty" cty:"tags"`
	Lifecycle              *terraformLifecycle  `json:"lifecycle,omitempty" cty:"lifecycle"`

	MinSize         *int64 `json:"min_size,omitempty" cty:"min_size"`
	MaxSize         *int64 `json:"max_size,omitempty" cty:"max_size"`
	DesiredCapacity *int64 `json:"desired_capacity,omitempty" cty:"desired_capacity"`

	SpotPercentage           *float64 `json:"spot_percentage,omitempty" cty:"spot_percentage"`
	FallbackToOnDemand       *bool    `json:"fallback_to_ondemand,omitempty" cty:"fallback_to_ondemand"`
	UtilizeReservedInstances *bool    `json:"utilize_reserved_instances,omitempty" cty:"utilize_reserved_instances"`
	DrainingTimeout          *int64   `json:"draining_timeout,omitempty" cty:"draining_timeout"`
	GracePeriod              *int64   `json:"grace_period,omitempty" cty:"grace_period"`

	Monitoring               *bool                          `json:"monitoring,omitempty" cty:"monitoring"`
	EBSOptimized             *bool                          `json:"ebs_optimized,omitempty" cty:"ebs_optimized"`
	ImageID                  *string                        `json:"image_id,omitempty" cty:"image_id"`
	AssociatePublicIPAddress *bool                          `json:"associate_public_ip_address,omitempty" cty:"associate_public_ip_address"`
	RootVolumeSize           *int32                         `json:"root_volume_size,omitempty" cty:"root_volume_size"`
	UserData                 *terraform.Literal             `json:"user_data,omitempty" cty:"user_data"`
	IAMInstanceProfile       *terraform.Literal             `json:"iam_instance_profile,omitempty" cty:"iam_instance_profile"`
	KeyName                  *terraform.Literal             `json:"key_name,omitempty" cty:"key_name"`
	SecurityGroups           []*terraform.Literal           `json:"security_groups,omitempty" cty:"security_groups"`
	Taints                   []*corev1.Taint                `json:"taints,omitempty" cty:"taints"`
	Labels                   []*terraformKV                 `json:"labels,omitempty" cty:"labels"`
	Headrooms                []*terraformAutoScalerHeadroom `json:"autoscale_headrooms,omitempty" cty:"autoscale_headrooms"`
}

func (_ *Ocean) RenderTerraform(t *terraform.TerraformTarget, a, e, changes *Ocean) error {
	cloud := t.Cloud.(awsup.AWSCloud)
	e.applyDefaults()

	tf := &terraformOcean{
		Name:   e.Name,
		Region: fi.String(cloud.Region()),

		DesiredCapacity: e.MinSize,
		MinSize:         e.MinSize,
		MaxSize:         e.MaxSize,

		SpotPercentage:           e.SpotPercentage,
		FallbackToOnDemand:       e.FallbackToOnDemand,
		UtilizeReservedInstances: e.UtilizeReservedInstances,
		DrainingTimeout:          e.DrainingTimeout,
		GracePeriod:              e.GracePeriod,
	}

	// Image.
	if e.ImageID != nil {
		image, err := resolveImage(cloud, fi.StringValue(e.ImageID))
		if err != nil {
			return err
		}
		tf.ImageID = image.ImageId
	}

	var role string
	for key := range e.Tags {
		if strings.HasPrefix(key, awstasks.CloudTagInstanceGroupRolePrefix) {
			suffix := strings.TrimPrefix(key, awstasks.CloudTagInstanceGroupRolePrefix)
			if role != "" && role != suffix {
				return fmt.Errorf("spotinst: found multiple role tags %q vs %q", role, suffix)
			}
			role = suffix
		}
	}

	// Security groups.
	if e.SecurityGroups != nil {
		for _, sg := range e.SecurityGroups {
			tf.SecurityGroups = append(tf.SecurityGroups, sg.TerraformLink())
			if role != "" {
				if err := t.AddOutputVariableArray(role+"_security_groups", sg.TerraformLink()); err != nil {
					return err
				}
			}
		}
	}

	// User data.
	if e.UserData != nil {
		var err error
		tf.UserData, err = t.AddFile("spotinst_ocean_aws", *e.Name, "user_data", e.UserData, false)
		if err != nil {
			return err
		}
	}

	// Public IP.
	if e.AssociatePublicIP != nil {
		tf.AssociatePublicIPAddress = e.AssociatePublicIP
	}

	// Root volume options.
	if opts := e.RootVolumeOpts; opts != nil {

		// Volume size.
		if opts.Size != nil {
			tf.RootVolumeSize = opts.Size
		}

		// EBS optimization.
		if opts.Optimization != nil {
			tf.EBSOptimized = opts.Optimization
		}
	}

	// IAM instance profile.
	if e.IAMInstanceProfile != nil {
		tf.IAMInstanceProfile = e.IAMInstanceProfile.TerraformLink()
	}

	// Monitoring.
	if e.Monitoring != nil {
		tf.Monitoring = e.Monitoring
	}

	// SSH key.
	if e.SSHKey != nil {
		tf.KeyName = e.SSHKey.TerraformLink()
	}

	// Subnets.
	if e.Subnets != nil {
		for _, subnet := range e.Subnets {
			tf.SubnetIDs = append(tf.SubnetIDs, subnet.TerraformLink())
			if role != "" {
				if err := t.AddOutputVariableArray(role+"_subnet_ids", subnet.TerraformLink()); err != nil {
					return err
				}
			}
		}
	}

	// Instance types.
	{
		// Whitelist.
		if e.InstanceTypesWhitelist != nil {
			tf.InstanceTypesWhitelist = e.InstanceTypesWhitelist
		}

		// Blacklist.
		if e.InstanceTypesBlacklist != nil {
			tf.InstanceTypesBlacklist = e.InstanceTypesBlacklist
		}
	}

	// Auto Scaler.
	{
		if opts := e.AutoScalerOpts; opts != nil {
			tf.ControllerClusterID = opts.ClusterID

			if opts.Enabled != nil {
				tf.AutoScaler = &terraformAutoScaler{
					Enabled:    opts.Enabled,
					AutoConfig: fi.Bool(true),
					Cooldown:   opts.Cooldown,
				}

				// Headroom.
				if headroom := opts.Headroom; headroom != nil {
					tf.AutoScaler.AutoConfig = fi.Bool(false)
					tf.AutoScaler.Headroom = &terraformAutoScalerHeadroom{
						CPUPerUnit: headroom.CPUPerUnit,
						GPUPerUnit: headroom.GPUPerUnit,
						MemPerUnit: headroom.MemPerUnit,
						NumOfUnits: headroom.NumOfUnits,
					}
				}

				// Scale down.
				if down := opts.Down; down != nil {
					tf.AutoScaler.Down = &terraformAutoScalerDown{
						MaxPercentage:     down.MaxPercentage,
						EvaluationPeriods: down.EvaluationPeriods,
					}
				}

				// Ignore capacity changes because the auto scaler updates the
				// desired capacity overtime.
				if fi.BoolValue(tf.AutoScaler.Enabled) {
					tf.Lifecycle = &terraformLifecycle{
						IgnoreChanges: []string{
							"desired_capacity",
						},
					}
				}
			}
		}
	}

	// Tags.
	{
		if e.Tags != nil {
			for _, tag := range e.buildTags() {
				tf.Tags = append(tf.Tags, &terraformKV{
					Key:   tag.Key,
					Value: tag.Value,
				})
			}
		}
	}

	return t.RenderResource("spotinst_ocean_aws", *e.Name, tf)
}

func (o *Ocean) TerraformLink() *terraform.Literal {
	return terraform.LiteralProperty("spotinst_ocean_aws", *o.Name, "id")
}

func (o *Ocean) buildTags() []*aws.Tag {
	tags := make([]*aws.Tag, 0, len(o.Tags))

	for key, value := range o.Tags {
		tags = append(tags, &aws.Tag{
			Key:   fi.String(key),
			Value: fi.String(value),
		})
	}

	return tags
}

func (o *Ocean) applyDefaults() {
	if o.SpotPercentage == nil {
		f := float64(100.0)
		o.SpotPercentage = &f
	}

	if o.FallbackToOnDemand == nil {
		o.FallbackToOnDemand = fi.Bool(true)
	}

	if o.UtilizeReservedInstances == nil {
		o.UtilizeReservedInstances = fi.Bool(true)
	}

	if o.Monitoring == nil {
		o.Monitoring = fi.Bool(false)
	}
}
