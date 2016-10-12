package ebs

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	util "github.com/chungers/infrakit-plugins/cmd/instance/aws"
	"github.com/docker/infrakit/spi/instance"
	"sort"
)

// TODO(chungers) -- Add a pool of instances to format the disks before making them
// available.

const (

	// TagName_InstanceLogicID is the name of an extra tag this plugin introduces to
	// store the logical ID of the volume
	TagName_InstanceLogicalID = "infrakit.instance.logicalID"
)

// CreateInstanceRequest is the concrete provision request type.
// This basically lets you add tags plus the input you'd normally provide
// here: http://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#CreateVolumeInput
type CreateInstanceRequest struct {
	DiscoverDefaults  bool
	Tags              map[string]string
	CreateVolumeInput ec2.CreateVolumeInput
}

// Provisioner is an instance provisioner for AWS EBS volumes.
type Provisioner struct {
	Client ec2iface.EC2API
}

// NewInstancePlugin creates a new plugin that creates instances in AWS EC2.
func NewInstancePlugin(client ec2iface.EC2API) instance.Plugin {
	return &Provisioner{Client: client}
}

// Validate performs local checks to determine if the request is valid.
func (p Provisioner) Validate(req json.RawMessage) error {
	model := &CreateInstanceRequest{}
	if err := json.Unmarshal(req, &model); err != nil {
		return err
	}
	return nil
}

// Provision creates a new instance.
func (p Provisioner) Provision(spec instance.Spec) (*instance.ID, error) {

	if spec.Properties == nil {
		return nil, errors.New("Properties must be set")
	}

	request := CreateInstanceRequest{}
	err := json.Unmarshal(*spec.Properties, &request)
	if err != nil {
		return nil, fmt.Errorf("Invalid input formatting: %s", err)
	}

	// This will default some fields to be identical to the instance running the plugin
	if request.DiscoverDefaults {
		setDiscoveredProperties(&request.CreateVolumeInput)
	}

	vol, err := p.Client.CreateVolume(&request.CreateVolumeInput)
	if err != nil {
		return nil, err
	}

	if vol.VolumeId == nil {
		return nil, fmt.Errorf("aws did not respond with volume id.")
	}

	// now tag the volume
	err = p.tagInstance(vol, &spec, request.Tags)
	if err != nil {
		return nil, err
	}

	instanceID := instance.ID(*vol.VolumeId)
	return &instanceID, nil
}

// Destroy terminates an existing instance.
func (p Provisioner) Destroy(id instance.ID) error {

	// TODO(chungers) -- Need to query about the status of the volume to make
	// sure it's ok to destroy.
	volID := string(id)
	input := ec2.DeleteVolumeInput{
		VolumeId: &volID,
	}
	_, err := p.Client.DeleteVolume(&input)
	return err
}

// DescribeInstances implements instance.Provisioner.DescribeInstances.
func (p Provisioner) DescribeInstances(tags map[string]string) ([]instance.Description, error) {
	return p.describeInstances(tags, nil)
}

func (p Provisioner) tagInstance(instance *ec2.Volume, spec *instance.Spec, userTags map[string]string) error {

	systemTags := map[string]string{}
	if spec.Tags != nil {
		systemTags = spec.Tags
	}
	if spec.LogicalID != nil {
		systemTags[TagName_InstanceLogicalID] = string(*spec.LogicalID)
	}

	ec2Tags := []*ec2.Tag{}

	// Gather the tag keys in sorted order, to provide predictable tag order.  This is
	// particularly useful for tests.
	var keys []string
	for k := range userTags {
		keys = append(keys, k)
	}
	if spec.Tags != nil {
		for k := range systemTags {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		// System tags overwrite user tags.
		key := k
		value, exists := systemTags[key]
		if !exists {
			value = userTags[key]
		}
		ec2Tags = append(ec2Tags, &ec2.Tag{Key: &key, Value: &value})
	}

	_, err := p.Client.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{instance.VolumeId},
		Tags:      ec2Tags,
	})
	return err
}

func setDiscoveredProperties(ri *ec2.CreateVolumeInput) {
	if ri.AvailabilityZone == nil {
		// No az specified, use the one I am in...
		az, err := util.GetMetadata(util.MetadataAvailabilityZone)
		if err == nil {
			ri.AvailabilityZone = &az
		}
	}
}

func (p Provisioner) describeInstances(tags map[string]string, nextToken *string) ([]instance.Description, error) {
	result, err := p.Client.DescribeVolumes(describeVolumesRequest(tags, nextToken))
	if err != nil {
		return nil, err
	}

	descriptions := []instance.Description{}
	for _, vol := range result.Volumes {

		tags := map[string]string{}
		if vol.Tags != nil {
			for _, tag := range vol.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = *tag.Value
				}
			}
		}

		descriptions = append(descriptions, instance.Description{
			ID:        instance.ID(*vol.VolumeId),
			LogicalID: logicalIDFromTags(vol.Tags),
			Tags:      tags,
		})
	}

	if result.NextToken != nil {
		// There are more pages of results.
		remainingPages, err := p.describeInstances(tags, result.NextToken)
		if err != nil {
			return nil, err
		}
		descriptions = append(descriptions, remainingPages...)
	}

	return descriptions, nil
}

func describeVolumesRequest(tags map[string]string, nextToken *string) *ec2.DescribeVolumesInput {
	filters := []*ec2.Filter{
		{
			Name: aws.String("status"),
			Values: []*string{
				aws.String("creating"),
				aws.String("available"),
				aws.String("in-use"),
			},
		},
	}
	for key, value := range tags {
		filters = append(filters, &ec2.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", key)),
			Values: []*string{aws.String(value)},
		})
	}

	return &ec2.DescribeVolumesInput{NextToken: nextToken, Filters: filters}
}

func addLogicalIDToTags(tags []*ec2.Tag, id *instance.LogicalID) []*ec2.Tag {
	if id == nil {
		return tags
	}

	key := TagName_InstanceLogicalID
	value := string(*id)

	return append(tags, &ec2.Tag{
		Key:   &key,
		Value: &value,
	})
}

func logicalIDFromTags(tags []*ec2.Tag) *instance.LogicalID {
	if tags == nil {
		return nil
	}

	for _, tag := range tags {
		if tag.Key != nil && *tag.Key == TagName_InstanceLogicalID {
			if tag.Value != nil {
				id := instance.LogicalID(*tag.Value)
				return &id
			}
		}
	}
	return nil
}
