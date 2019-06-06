package kubernetes

import (
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	api "k8s.io/api/scheduling/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgApi "k8s.io/apimachinery/pkg/types"
	kubernetes "k8s.io/client-go/kubernetes"
)

func resourceKubernetesPriorityClass() *schema.Resource {
	return &schema.Resource{
		Create: resourceKubernetesPriorityClassCreate,
		Read:   resourceKubernetesPriorityClassRead,
		Exists: resourceKubernetesPriorityClassExists,
		Update: resourceKubernetesPriorityClassUpdate,
		Delete: resourceKubernetesPriorityClassDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"metadata": metadataSchema("priority class", true),
			"description": {
				Type:        schema.TypeString,
				Description: "An arbitrary string that usually provides guidelines on when this priority class should be used.",
				Optional:    true,
				Default:     "",
			},
			"global_default": {
				Type:        schema.TypeBool,
				Description: "Specifies whether this PriorityClass should be considered as the default priority for pods that do not have any priority class. Only one PriorityClass can be marked as `globalDefault`. However, if more than one PriorityClasses exists with their `globalDefault` field set to true, the smallest value of such global default PriorityClasses will be used as the default priority.",
				Optional:    true,
				Default:     false,
			},
			"value": {
				Type:        schema.TypeInt,
				Description: "The value of this priority class. This is the actual priority that pods receive when they have the name of this class in their pod spec.",
				Required:    true,
			},
		},
	}
}

func resourceKubernetesPriorityClassCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*kubernetes.Clientset)

	metadata := expandMetadata(d.Get("metadata").([]interface{}))
	value := d.Get("value").(int32)
	description := d.Get("description").(string)
	globalDefault := d.Get("global_default").(bool)

	priorityClass := api.PriorityClass{
		ObjectMeta:    metadata,
		Description:   description,
		GlobalDefault: globalDefault,
		Value:         value,
	}

	log.Printf("[INFO] Creating new priority class: %#v", priorityClass)
	out, err := conn.Scheduling().PriorityClasses().Create(&priorityClass)
	if err != nil {
		return fmt.Errorf("Failed to create priority class: %s", err)
	}
	log.Printf("[INFO] Submitted new priority class: %#v", out)
	d.SetId(buildId(out.ObjectMeta))

	err = resource.Retry(1*time.Minute, func() *resource.RetryError {
		createdPriorityClass, err := conn.Scheduling().PriorityClasses().Get(out.Name, meta_v1.GetOptions{})
		if err != nil {
			return resource.NonRetryableError(err)
		}
		if createdPriorityClass.Value == priorityClass.Value {
			return nil
		}
		err = fmt.Errorf("Priority class doesn't match after creation.\nExpected: %#v\nGiven: %#v",
			createdPriorityClass.Value, priorityClass.Value)
		return resource.RetryableError(err)
	})
	if err != nil {
		return err
	}

	return resourceKubernetesPriorityClassRead(d, meta)
}

func resourceKubernetesPriorityClassRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*kubernetes.Clientset)
	name := d.Id()

	log.Printf("[INFO] Reading priority class %s", name)
	priorityClass, err := conn.Scheduling().PriorityClasses().Get(name, meta_v1.GetOptions{})
	if err != nil {
		log.Printf("[DEBUG] Received error: %#v", err)
		return err
	}
	log.Printf("[INFO] Received priority class: %#v", priorityClass)

	// This is to work around K8S bug
	// See https://github.com/kubernetes/kubernetes/issues/44539
	if priorityClass.ObjectMeta.GenerateName == "" {
		if v, ok := d.GetOk("metadata.0.generate_name"); ok {
			priorityClass.ObjectMeta.GenerateName = v.(string)
		}
	}

	err = d.Set("metadata", flattenMetadata(priorityClass.ObjectMeta, d))
	if err != nil {
		return err
	}

	err = d.Set("value", priorityClass.Value)
	if err != nil {
		return err
	}

	err = d.Set("description", priorityClass.Description)
	if err != nil {
		return err
	}

	err = d.Set("global_default", priorityClass.GlobalDefault)
	if err != nil {
		return err
	}

	return nil
}

func resourceKubernetesPriorityClassUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*kubernetes.Clientset)
	name := d.Id()

	ops := patchMetadata("metadata.0.", "/metadata/", d)

	if d.HasChange("value") {
		value := d.Get("value").(int32)
		ops = append(ops, &ReplaceOperation{
			Path:  "/value",
			Value: value,
		})
	}

	if d.HasChange("description") {
		description := d.Get("description").(string)
		ops = append(ops, &ReplaceOperation{
			Path:  "/description",
			Value: description,
		})
	}

	if d.HasChange("global_default") {
		globalDefault := d.Get("global_default").(string)
		ops = append(ops, &ReplaceOperation{
			Path:  "/globalDefault",
			Value: globalDefault,
		})
	}

	data, err := ops.MarshalJSON()
	if err != nil {
		return fmt.Errorf("Failed to marshal update operations: %s", err)
	}
	log.Printf("[INFO] Updating priority class %q: %v", name, string(data))
	out, err := conn.Scheduling().PriorityClasses().Patch(name, pkgApi.JSONPatchType, data)
	if err != nil {
		return fmt.Errorf("Failed to update priority class: %s", err)
	}
	log.Printf("[INFO] Submitted updated priority class: %#v", out)
	d.SetId(buildId(out.ObjectMeta))

	return resourceKubernetesPriorityClassRead(d, meta)
}

func resourceKubernetesPriorityClassDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*kubernetes.Clientset)
	name := d.Id()

	log.Printf("[INFO] Deleting priority class: %#v", name)
	err := conn.Scheduling().PriorityClasses().Delete(name, &meta_v1.DeleteOptions{})
	if err != nil {
		return err
	}

	log.Printf("[INFO] priority class %s deleted", name)

	d.SetId("")
	return nil
}

func resourceKubernetesPriorityClassExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	conn := meta.(*kubernetes.Clientset)
	name := d.Id()

	log.Printf("[INFO] Checking priority class %s", name)
	_, err := conn.Scheduling().PriorityClasses().Get(name, meta_v1.GetOptions{})
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.ErrStatus.Code == 404 {
			return false, nil
		}
		log.Printf("[DEBUG] Received error: %#v", err)
	}
	return true, err
}
