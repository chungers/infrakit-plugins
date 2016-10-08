InfraKit Instance Plugin - AWS EBS Volume
=========================================

This is a simple instance plugin that will provision an EBS Volume on AWS.
With a group plugin, you can provision a pool of volumes and then make
these volumes available to the EC2 instances you provision in another group.

  + It is able to "discover" the region, availability zone of the current
  instance where the plugin is running and use those as values if they are not specified in the
  input.
  + Of course, you can override it.  See the example [`instance-properties.json`](instance-properties.json)

## TODO

  + At the moment, this simply provisions the EBS volume without actually formatting the media.
So we'd like to include in this plugin the ability to maintain a pool of small instances that
can be used to format the volumes and possibly prepare any data necessary on the volume.
  + Need to work out a simple convention that instances that have attachments will not destroy
  the attachments but rather we let the group driver determine which unused volumes to destroy.
  + We are implementing a way for another flavor plugin to select a specific or any instance from
  a group of resources. This then allows the group that is provisioning the nodes (hosts) to be
  able to specify a flavor plugin that can actually pull a volume (a properly formatted one) from
  the EBS volume group and then attach it to an EC2 instance.  This will be very useful for managing
  Docker Swarm worker nodes where we can have container state survive even as we replace/ upgrade
  the EC2 instance.  Then it would be possible to rehyrdate the container and resume running (via
  checkpoint and restore).  To the best of my knowledge this is not possible with ASG (auto-scaling
  groups) on AWS.

## Name of the plugin

The name can be set based on the unix socket in the `listen` flag when starting up the plugin.  Currently,
the name defaults to `aws-ebs` (meaning you'd see a file named `aws-ebs.sock` in `/run/infrakit/plugins` or
whatever the plugin discovery directory you set (as part of the path of the `unix://` url in `listen` flag).


## Input to the Plugin -- the `Properties` block

The struct [`CreateInstanceRequest`](/plugin/instance/aws/ebs/plugin.go) is the Golang struct that is unmarshaled
from the opaque blob value of the field `Properties` in other JSON structure that uses this plugin.  It looks like
```go
type CreateInstanceRequest struct {
        DiscoverDefaults  bool
        Tags              map[string]string
        CreateVolumeInput ec2.CreateVolumeInput
}
```

Note that it's just made up of

  + a flag `DiscoverDefaults` which will turn on discovery and set some default values
so you don't have to.
  + An associative array (dictionary or map) of the tags you want to use
  + The [`CreateVolumeInput`](http://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#CreateVolumeInput)
   of the AWS EC2 API, in JSON form.

Here is an example,

```json
{
    "DiscoverDefaults" : true,
    "Tags" : {
        "env" : "dev",
        "instance-plugin" : "aws-ebs"
    },
    "CreateVolumeInput" : {
        "Size" : 100
    }
}
```

So when you put this in a larger JSON, say using this with a Group plugin, the config JSON would look like:

```json
{
    "ID": "aws_ebs_demo",
    "Properties": {
        "Instance" : {
            "Plugin": "aws-ebs",
            "Properties": {
                "DiscoverDefaults" : true,
                "Tags" : {
                    "env" : "dev",
                    "instance-plugin" : "aws-ebs"
                },
                "CreateVolumeInput" : {
                    "Size" : 100
                }
            }
        },
        "Flavor" : {
            "Plugin": "flavor-vanilla",
            "Properties": {
                "Size" : 5
            }
        }
    }
}
```
In the example above, we have a group of 5 volumes that need to be provisioned, each with 100G of capacity.

To have the Group plugin watch this group, first make sure the plugins are all running (see tutorial and examples elsewhere).
Then, do this:

```
$ infrakit/cli group watch group.json
```

InfraKit will create the new volumes if they aren't around already.

## Environment Discovery

This plugin uses the [AWS Instance Metadata Service](http://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.htm)
to discover the local environment so you don't have to tell it.
It sets the following to discovered values if the values are missing in the `Properties` block:

  + The region (discovered unless set at start up of plugin)
  + The Availability Zone (`AvailabilityZone *string`)
