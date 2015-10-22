package main

import (
	"fmt"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/joyent/gosdc/cloudapi"
	"reflect"
	"regexp"
	"time"
)

var (
	machineStateRunning = "running"
	machineStateStopped = "stopped"

	machineStateChangeTimeout       = 10 * time.Minute
	machineStateChangeCheckInterval = 10 * time.Second

	resourceMachineMetadataKeys = map[string]string{
		// semantics: "schema_name": "metadata_name"
		"root_authorized_keys": "root_authorized_keys",
		"user_script":          "user-script",
		"user_data":            "user-data",
		"administrator_pw":     "administrator-pw",
	}
)

func resourceMachine() *schema.Resource {
	return &schema.Resource{
		Create: wrapCallback(resourceMachineCreate),
		Exists: wrapExistsCallback(resourceMachineExists),
		Read:   wrapCallback(resourceMachineRead),
		Update: wrapCallback(resourceMachineUpdate),
		Delete: wrapCallback(resourceMachineDelete),

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Description:  "friendly name",
				Type:         schema.TypeString,
				Computed:     true,
				ValidateFunc: resourceMachineValidateName,
			},
			"type": &schema.Schema{
				Description: "machine type (smartmachine or virtualmachine)",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"state": &schema.Schema{
				Description: "current state of the machine",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"dataset": &schema.Schema{
				Description: "dataset URN the machine was provisioned with",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"memory": &schema.Schema{
				Description: "amount of memory the machine has (in Mb)",
				Type:        schema.TypeInt,
				Computed:    true,
			},
			"disk": &schema.Schema{
				Description: "amount of disk the machine has (in Gb)",
				Type:        schema.TypeInt,
				Computed:    true,
			},
			"ips": &schema.Schema{
				Description: "IP addresses the machine has",
				Type:        schema.TypeList,
				Computed:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"tags": &schema.Schema{
				Description: "machine tags",
				Type:        schema.TypeMap,
				Optional:    true,
			},
			"created": &schema.Schema{
				Description: "when the machine was created",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"updated": &schema.Schema{
				Description: "when the machine was update",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"package": &schema.Schema{
				Description: "name of the pakcage to use on provisioning",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true, // TODO: remove when Update is added
				// TODO: validate that the package is available
			},
			"image": &schema.Schema{
				Description: "image UUID",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true, // TODO: remove when Update is added
				// TODO: validate that the UUID is valid
			},
			"primaryip": &schema.Schema{
				Description: "the primary (public) IP address for the machine",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"networks": &schema.Schema{
				Description: "desired network IDs",
				Type:        schema.TypeList,
				Optional:    true,
				ForceNew:    true, // TODO: remove when Update is added
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				// Default:     []string{"public", "private"},
				// TODO: validate that a valid network is presented
			},
			// TODO: firewall_enabled

			// computed resources from metadata
			"root_authorized_keys": &schema.Schema{
				Description: "authorized keys for the root user on this machine",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"user_script": &schema.Schema{
				Description: "user script to run on boot (every boot on SmartMachines)",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"user_data": &schema.Schema{
				Description: "copied to machine on boot",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"administrator_pw": &schema.Schema{
				Description: "administrator's initial password (Windows only)",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

func resourceMachineCreate(d ResourceData, config *Config) error {
	api, err := config.Cloud()
	if err != nil {
		return err
	}

	var networks []string
	for _, network := range d.Get("networks").([]interface{}) {
		networks = append(networks, network.(string))
	}

	metadata := map[string]string{}
	for schemaName, metadataKey := range resourceMachineMetadataKeys {
		if v, ok := d.GetOk(schemaName); ok {
			metadata[metadataKey] = v.(string)
		}
	}

	tags := map[string]string{}
	for k, v := range d.Get("tags").(map[string]interface{}) {
		tags[k] = v.(string)
	}

	machine, err := api.CreateMachine(cloudapi.CreateMachineOpts{
		Name:            d.Get("name").(string),
		Package:         d.Get("package").(string),
		Image:           d.Get("image").(string),
		Networks:        networks,
		Metadata:        metadata,
		Tags:            tags,
		FirewallEnabled: true, // TODO: turn this into another schema field
	})
	if err != nil {
		return err
	}

	err = waitForMachineState(api, machine.Id, machineStateRunning, machineStateChangeTimeout)
	if err != nil {
		return err
	}

	// refresh state after it provisions
	d.SetId(machine.Id)
	err = resourceMachineRead(d, config)
	if err != nil {
		return err
	}

	return nil
}

func resourceMachineExists(d ResourceData, config *Config) (bool, error) {
	api, err := config.Cloud()
	if err != nil {
		return false, err
	}

	machine, err := api.GetMachine(d.Id())

	return machine != nil && err == nil, err
}

func resourceMachineRead(d ResourceData, config *Config) error {
	api, err := config.Cloud()
	if err != nil {
		return err
	}

	machine, err := api.GetMachine(d.Id())
	if err != nil {
		return err
	}

	d.SetId(machine.Id)
	d.Set("name", machine.Name)
	d.Set("type", machine.Type)
	d.Set("state", machine.State)
	d.Set("dataset", machine.Dataset)
	d.Set("memory", machine.Memory)
	d.Set("disk", machine.Disk)
	d.Set("ips", machine.IPs)
	d.Set("tags", machine.Tags)
	d.Set("created", machine.Created)
	d.Set("updated", machine.Updated)
	d.Set("package", machine.Package)
	d.Set("image", machine.Image)
	d.Set("primaryip", machine.PrimaryIP)
	d.Set("networks", machine.Networks)

	// computed attributes from metadata
	for schemaName, metadataKey := range resourceMachineMetadataKeys {
		d.Set(schemaName, machine.Metadata[metadataKey])
	}

	return nil
}

func resourceMachineUpdate(d ResourceData, config *Config) error {
	api, err := config.Cloud()
	if err != nil {
		return err
	}

	d.Partial(true)

	if d.HasChange("name") {
		if err := api.RenameMachine(d.Id(), d.Get("name").(string)); err != nil {
			return err
		}

		err := waitFor(
			func() (bool, error) {
				machine, err := api.GetMachine(d.Id())
				return machine.Name == d.Get("name").(string), err
			},
			machineStateChangeCheckInterval,
			1*time.Minute,
		)
		if err != nil {
			return err
		}

		d.SetPartial("name")
	}

	if d.HasChange("tags") {
		tags := map[string]string{}
		for k, v := range d.Get("tags").(map[string]interface{}) {
			tags[k] = v.(string)
		}

		var newTags map[string]string
		if len(tags) == 0 {
			err = api.DeleteMachineTags(d.Id())
			newTags = map[string]string{}
		} else {
			newTags, err = api.ReplaceMachineTags(d.Id(), tags)
		}
		if err != nil {
			return err
		}

		err = waitFor(
			func() (bool, error) {
				machine, err := api.GetMachine(d.Id())
				return reflect.DeepEqual(machine.Tags, newTags), err
			},
			machineStateChangeCheckInterval,
			1*time.Minute,
		)
		if err != nil {
			return err
		}

		// this API endpoint returns the new tags. To avoid getting into an
		// inconsistent state (if the state is changed remotely in response to the
		// change here) we're going to copy the remote tags to our local tags before
		// saying everything is OK.
		iNewTags := map[string]interface{}{}
		for k, v := range newTags {
			iNewTags[k] = v
		}
		d.Set("tags", iNewTags)

		d.SetPartial("tags")
	}

	d.Partial(false)

	return nil
}

func resourceMachineDelete(d ResourceData, config *Config) error {
	api, err := config.Cloud()
	if err != nil {
		return err
	}

	state, err := readMachineState(api, d.Id())
	if state != machineStateStopped {
		err = api.StopMachine(d.Id())
		if err != nil {
			return err
		}

		waitForMachineState(api, d.Id(), machineStateStopped, machineStateChangeTimeout)
	}

	err = api.DeleteMachine(d.Id())
	if err != nil {
		return err
	}

	d.SetId("")

	return nil
}

func readMachineState(api *cloudapi.Client, id string) (string, error) {
	machine, err := api.GetMachine(id)
	if err != nil {
		return "", err
	}

	return machine.State, nil
}

// waitForMachineState waits for a machine to be in the desired state (waiting
// some seconds between each poll). If it doesn't reach the state within the
// duration specified in `timeout`, it returns ErrMachineStateTimeout.
func waitForMachineState(api *cloudapi.Client, id, state string, timeout time.Duration) error {
	return waitFor(
		func() (bool, error) {
			currentState, err := readMachineState(api, id)
			return currentState == state, err
		},
		machineStateChangeCheckInterval,
		machineStateChangeTimeout,
	)
}

func resourceMachineValidateName(value interface{}, name string) (warnings []string, errors []error) {
	warnings = []string{}
	errors = []error{}

	r := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\_\.\-]*$`)
	if !r.Match([]byte(value.(string))) {
		errors = append(errors, fmt.Errorf(`"%s" is not a valid %s`, value.(string), name))
	}

	return warnings, errors
}
