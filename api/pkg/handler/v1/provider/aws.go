package provider

import (
	"context"
	"fmt"
	"net/http"

	ec2 "github.com/cristim/ec2-instances-info"
	"github.com/go-kit/kit/endpoint"
	"github.com/gorilla/mux"

	apiv1 "github.com/kubermatic/kubermatic/api/pkg/api/v1"
	kubermaticv1 "github.com/kubermatic/kubermatic/api/pkg/crd/kubermatic/v1"
	"github.com/kubermatic/kubermatic/api/pkg/handler/middleware"
	"github.com/kubermatic/kubermatic/api/pkg/handler/v1/common"
	"github.com/kubermatic/kubermatic/api/pkg/handler/v1/dc"
	"github.com/kubermatic/kubermatic/api/pkg/provider"
	awsprovider "github.com/kubermatic/kubermatic/api/pkg/provider/cloud/aws"
	kubernetesprovider "github.com/kubermatic/kubermatic/api/pkg/provider/kubernetes"
	"github.com/kubermatic/kubermatic/api/pkg/util/errors"
)

var data *ec2.InstanceData

// Due to big amount of data we are loading AWS instance types only once. Do not edit it.
func init() {
	data, _ = ec2.Data()
}

// AWSCommonReq represent a request with common parameters for AWS.
type AWSCommonReq struct {
	// in: header
	// name: AccessKeyID
	AccessKeyID string
	// in: header
	// name: SecretAccessKey
	SecretAccessKey string
	// in: header
	// name: Credential
	Credential string
}

// AWSSubnetReq represent a request for AWS subnets.
// swagger:parameters listAWSSubnets
type AWSSubnetReq struct {
	AWSCommonReq
	// in: path
	// required: true
	DC string `json:"dc"`
	// in: header
	// name: VPC
	VPC string `json:"vpc"`
}

// AWSVPCReq represent a request for AWS vpc's.
// swagger:parameters listAWSVPCS
type AWSVPCReq struct {
	AWSCommonReq
	// in: path
	// required: true
	DC string `json:"dc"`
}

// AWSSizeReq represent a request for AWS VM sizes.
// swagger:parameters listAWSSizes
type AWSSizeReq struct {
	Region string
}

// DecodeAWSSizesReq decodes the base type for a AWS special endpoint request
func DecodeAWSSizesReq(c context.Context, r *http.Request) (interface{}, error) {
	var req AWSSizeReq
	req.Region = r.Header.Get("Region")
	return req, nil
}

// DecodeAWSCommonReq decodes the base type for a AWS special endpoint request
func DecodeAWSCommonReq(c context.Context, r *http.Request) (interface{}, error) {
	var req AWSCommonReq

	req.AccessKeyID = r.Header.Get("AccessKeyID")
	req.SecretAccessKey = r.Header.Get("SecretAccessKey")
	req.Credential = r.Header.Get("Credential")

	return req, nil
}

// DecodeAWSSubnetReq decodes a request for a list of AWS subnets
func DecodeAWSSubnetReq(c context.Context, r *http.Request) (interface{}, error) {
	var req AWSSubnetReq

	commonReq, err := DecodeAWSCommonReq(c, r)
	if err != nil {
		return nil, err
	}
	req.AWSCommonReq = commonReq.(AWSCommonReq)

	dc, ok := mux.Vars(r)["dc"]
	if !ok {
		return req, fmt.Errorf("'dc' parameter is required")
	}
	req.DC = dc

	req.VPC = r.Header.Get("VPC")

	return req, nil
}

// DecodeAWSVPCReq decodes a request for a list of AWS vpc's
func DecodeAWSVPCReq(c context.Context, r *http.Request) (interface{}, error) {
	var req AWSVPCReq

	commonReq, err := DecodeAWSCommonReq(c, r)
	if err != nil {
		return nil, err
	}
	req.AWSCommonReq = commonReq.(AWSCommonReq)

	dc, ok := mux.Vars(r)["dc"]
	if !ok {
		return req, fmt.Errorf("'dc' parameter is required")
	}
	req.DC = dc

	return req, nil
}

// AWSSizeEndpoint handles the request to list available AWS sizes.
func AWSSizeEndpoint() endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(AWSSizeReq)
		return awsSizes(req.Region)
	}
}

// AWSSizeNoCredentialsEndpoint handles the request to list available AWS sizes.
func AWSSizeNoCredentialsEndpoint(projectProvider provider.ProjectProvider, seedsGetter provider.SeedsGetter) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(common.GetClusterReq)
		userInfo := ctx.Value(middleware.UserInfoContextKey).(*provider.UserInfo)
		clusterProvider := ctx.Value(middleware.ClusterProviderContextKey).(provider.ClusterProvider)
		_, err := projectProvider.Get(userInfo, req.ProjectID, &provider.ProjectGetOptions{})
		if err != nil {
			return nil, common.KubernetesErrorToHTTPError(err)
		}
		cluster, err := clusterProvider.Get(userInfo, req.ClusterID, &provider.ClusterGetOptions{})
		if err != nil {
			return nil, common.KubernetesErrorToHTTPError(err)
		}
		if cluster.Spec.Cloud.AWS == nil {
			return nil, errors.NewNotFound("cloud spec for ", req.ClusterID)
		}

		dc, err := dc.GetDatacenter(seedsGetter, cluster.Spec.Cloud.DatacenterName)
		if err != nil {
			return nil, errors.New(http.StatusInternalServerError, err.Error())
		}

		if dc.Spec.AWS == nil {
			return nil, errors.NewNotFound("cloud spec (dc) for ", req.ClusterID)
		}

		return awsSizes(dc.Spec.AWS.Region)
	}
}

func awsSizes(region string) (apiv1.AWSSizeList, error) {
	if data == nil {
		return nil, fmt.Errorf("AWS instance type data not initialized")
	}

	sizes := apiv1.AWSSizeList{}
	for _, i := range *data {
		// TODO: Make the check below more generic, working for all the providers. It is needed as the pods
		//  with memory under 2 GB will be full with required pods like kube-proxy, CNI etc.
		if i.Memory >= 2 {
			pricing, ok := i.Pricing[region]
			if !ok {
				continue
			}

			// Filter out unavailable or too expensive instance types (>1$ per hour).
			price := pricing.Linux.OnDemand
			if price == 0 || price > 1 {
				continue
			}

			sizes = append(sizes, apiv1.AWSSize{
				Name:       i.InstanceType,
				PrettyName: i.PrettyName,
				Memory:     i.Memory,
				VCPUs:      i.VCPU,
				Price:      price,
			})
		}
	}

	return sizes, nil
}

// AWSSubnetEndpoint handles the request to list AWS availability subnets in a given vpc, using provided credentials
func AWSSubnetEndpoint(credentialManager common.PresetsManager, seedsGetter provider.SeedsGetter) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(AWSSubnetReq)

		accessKeyID := req.AccessKeyID
		secretAccessKey := req.SecretAccessKey
		vpcID := req.VPC

		userInfo, ok := ctx.Value(middleware.UserInfoContextKey).(*provider.UserInfo)
		if !ok {
			return nil, errors.New(http.StatusInternalServerError, "can not get user info")
		}

		if len(req.Credential) > 0 {
			preset, err := credentialManager.GetPreset(userInfo, req.Credential)
			if err != nil {
				return nil, errors.New(http.StatusInternalServerError, fmt.Sprintf("can not get preset %s for user %s", req.Credential, userInfo.Email))
			}
			if credential := preset.Spec.AWS; credential != nil {
				accessKeyID = credential.AccessKeyID
				secretAccessKey = credential.SecretAccessKey
				vpcID = credential.VPCID
			}
		}

		seeds, err := seedsGetter()
		if err != nil {
			return nil, errors.New(http.StatusInternalServerError, fmt.Sprintf("failed to list seeds: %v", err))
		}
		_, dc, err := provider.DatacenterFromSeedMap(seeds, req.DC)
		if err != nil {
			return nil, errors.NewBadRequest(err.Error())
		}

		return listAWSSubnets(accessKeyID, secretAccessKey, vpcID, dc)
	}
}

// AWSSubnetWithClusterCredentialsEndpoint handles the request to list AWS availability subnets in a given vpc, using credentials
func AWSSubnetWithClusterCredentialsEndpoint(projectProvider provider.ProjectProvider, seedsGetter provider.SeedsGetter) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(common.GetClusterReq)
		clusterProvider := ctx.Value(middleware.ClusterProviderContextKey).(provider.ClusterProvider)
		userInfo := ctx.Value(middleware.UserInfoContextKey).(*provider.UserInfo)
		_, err := projectProvider.Get(userInfo, req.ProjectID, &provider.ProjectGetOptions{})
		if err != nil {
			return nil, common.KubernetesErrorToHTTPError(err)
		}
		cluster, err := clusterProvider.Get(userInfo, req.ClusterID, &provider.ClusterGetOptions{})
		if err != nil {
			return nil, common.KubernetesErrorToHTTPError(err)
		}
		if cluster.Spec.Cloud.AWS == nil {
			return nil, errors.NewNotFound("cloud spec for ", req.ClusterID)
		}

		seeds, err := seedsGetter()
		if err != nil {
			return nil, errors.New(http.StatusInternalServerError, fmt.Sprintf("failed to list seeds: %v", err))
		}
		_, dc, err := provider.DatacenterFromSeedMap(seeds, cluster.Spec.Cloud.DatacenterName)
		if err != nil {
			return nil, errors.NewBadRequest(err.Error())
		}
		assertedClusterProvider, ok := clusterProvider.(*kubernetesprovider.ClusterProvider)
		if !ok {
			return nil, errors.New(http.StatusInternalServerError, "failed to assert clusterProvider")
		}

		secretKeySelector := provider.SecretKeySelectorValueFuncFactory(ctx, assertedClusterProvider.GetSeedClusterAdminRuntimeClient())
		accessKeyID, secretAccessKey, err := awsprovider.GetCredentialsForCluster(cluster.Spec.Cloud, secretKeySelector)
		if err != nil {
			return nil, err
		}

		return listAWSSubnets(accessKeyID, secretAccessKey, cluster.Spec.Cloud.AWS.VPCID, dc)
	}
}

func listAWSSubnets(accessKeyID, secretAccessKey, vpcID string, datacenter *kubermaticv1.Datacenter) (apiv1.AWSSubnetList, error) {

	if datacenter.Spec.AWS == nil {
		return nil, errors.NewBadRequest("datacenter is not an AWS datacenter")
	}

	subnetResults, err := awsprovider.GetSubnets(accessKeyID, secretAccessKey, datacenter.Spec.AWS.Region, vpcID)
	if err != nil {
		return nil, fmt.Errorf("couldn't get subnets: %v", err)
	}

	subnets := apiv1.AWSSubnetList{}
	for _, s := range subnetResults {
		subnetTags := []apiv1.AWSTag{}
		var subnetName string
		for _, v := range s.Tags {
			subnetTags = append(subnetTags, apiv1.AWSTag{Key: *v.Key, Value: *v.Value})
			if *v.Key == "Name" {
				subnetName = *v.Value
			}
		}

		// Even though Ipv6CidrBlockAssociationSet is defined as []*VpcIpv6CidrBlockAssociation in AWS,
		// it is currently not possible to use more than one cidr block.
		// In case there are blocks with state != associated, we check for it and use the first entry
		// that matches condition.
		var subnetIpv6 string
		for _, v := range s.Ipv6CidrBlockAssociationSet {
			if *v.Ipv6CidrBlockState.State == "associated" {
				subnetIpv6 = *v.Ipv6CidrBlock
				break
			}
		}

		subnets = append(subnets, apiv1.AWSSubnet{
			Name:                    subnetName,
			ID:                      *s.SubnetId,
			AvailabilityZone:        *s.AvailabilityZone,
			AvailabilityZoneID:      *s.AvailabilityZoneId,
			IPv4CIDR:                *s.CidrBlock,
			IPv6CIDR:                subnetIpv6,
			Tags:                    subnetTags,
			State:                   *s.State,
			AvailableIPAddressCount: *s.AvailableIpAddressCount,
			DefaultForAz:            *s.DefaultForAz,
		})

	}

	return subnets, nil
}

// AWSVPCEndpoint handles the request to list AWS VPC's, using provided credentials
func AWSVPCEndpoint(credentialManager common.PresetsManager, seedsGetter provider.SeedsGetter) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(AWSVPCReq)

		accessKeyID := req.AccessKeyID
		secretAccessKey := req.SecretAccessKey

		userInfo, ok := ctx.Value(middleware.UserInfoContextKey).(*provider.UserInfo)
		if !ok {
			return nil, errors.New(http.StatusInternalServerError, "can not get user info")
		}

		if len(req.Credential) > 0 {
			preset, err := credentialManager.GetPreset(userInfo, req.Credential)
			if err != nil {
				return nil, errors.New(http.StatusInternalServerError, fmt.Sprintf("can not get preset %s for user %s", req.Credential, userInfo.Email))
			}
			if credential := preset.Spec.AWS; credential != nil {
				accessKeyID = credential.AccessKeyID
				secretAccessKey = credential.SecretAccessKey
			}
		}

		seeds, err := seedsGetter()
		if err != nil {
			return nil, errors.New(http.StatusInternalServerError, fmt.Sprintf("failed to list seeds: %v", err))
		}
		_, datacenter, err := provider.DatacenterFromSeedMap(seeds, req.DC)
		if err != nil {
			return nil, errors.NewBadRequest(err.Error())
		}

		return listAWSVPCS(accessKeyID, secretAccessKey, datacenter)
	}
}

func listAWSVPCS(accessKeyID, secretAccessKey string, datacenter *kubermaticv1.Datacenter) (apiv1.AWSVPCList, error) {

	if datacenter.Spec.AWS == nil {
		return nil, errors.NewBadRequest("datacenter is not an AWS datacenter")
	}

	vpcsResults, err := awsprovider.GetVPCS(accessKeyID, secretAccessKey, datacenter.Spec.AWS.Region)
	if err != nil {
		return nil, fmt.Errorf("couldn't get vpc's: %v", err)
	}

	vpcs := apiv1.AWSVPCList{}
	for _, vpc := range vpcsResults {
		var tags []apiv1.AWSTag
		var cidrBlockList []apiv1.AWSVpcCidrBlockAssociation
		var Ipv6CidrBlocList []apiv1.AWSVpcIpv6CidrBlockAssociation
		var name string

		for _, tag := range vpc.Tags {
			tags = append(tags, apiv1.AWSTag{Key: *tag.Key, Value: *tag.Value})
			if *tag.Key == "Name" {
				name = *tag.Value
			}
		}

		for _, cidr := range vpc.CidrBlockAssociationSet {
			cidrBlock := apiv1.AWSVpcCidrBlockAssociation{
				AssociationID: *cidr.AssociationId,
				CidrBlock:     *cidr.CidrBlock,
			}
			if cidr.CidrBlockState != nil {
				if cidr.CidrBlockState.State != nil {
					cidrBlock.State = *cidr.CidrBlockState.State
				}
				if cidr.CidrBlockState.StatusMessage != nil {
					cidrBlock.StatusMessage = *cidr.CidrBlockState.StatusMessage
				}
			}
			cidrBlockList = append(cidrBlockList, cidrBlock)
		}

		for _, cidr := range vpc.Ipv6CidrBlockAssociationSet {
			cidrBlock := apiv1.AWSVpcIpv6CidrBlockAssociation{
				AWSVpcCidrBlockAssociation: apiv1.AWSVpcCidrBlockAssociation{
					AssociationID: *cidr.AssociationId,
					CidrBlock:     *cidr.Ipv6CidrBlock,
				},
			}
			if cidr.Ipv6CidrBlockState != nil {
				if cidr.Ipv6CidrBlockState.State != nil {
					cidrBlock.State = *cidr.Ipv6CidrBlockState.State
				}
				if cidr.Ipv6CidrBlockState.StatusMessage != nil {
					cidrBlock.StatusMessage = *cidr.Ipv6CidrBlockState.StatusMessage
				}
			}

			Ipv6CidrBlocList = append(Ipv6CidrBlocList, cidrBlock)
		}

		vpcs = append(vpcs, apiv1.AWSVPC{
			Name:                        name,
			VpcID:                       *vpc.VpcId,
			CidrBlock:                   *vpc.CidrBlock,
			DhcpOptionsID:               *vpc.DhcpOptionsId,
			InstanceTenancy:             *vpc.InstanceTenancy,
			IsDefault:                   *vpc.IsDefault,
			OwnerID:                     *vpc.OwnerId,
			State:                       *vpc.State,
			Tags:                        tags,
			Ipv6CidrBlockAssociationSet: Ipv6CidrBlocList,
			CidrBlockAssociationSet:     cidrBlockList,
		})

	}

	return vpcs, err
}