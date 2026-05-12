"""A Crossplane composition function."""

import grpc

import json

from crossplane.function import logging, response, resource
from crossplane.function.proto.v1 import run_function_pb2 as fnv1
from crossplane.function.proto.v1 import run_function_pb2_grpc as grpcv1

from models.io.k8s.apimachinery.pkg.apis.meta import v1 as metav1
from models.com.example.platform.storagebucket import v1alpha1
from models.io.upbound.aws.s3.bucket import v1beta1 as bucketv1beta1
from models.io.upbound.aws.s3.bucketpolicy import v1beta1 as policyv1beta1
from models.io.upbound.aws.s3.bucketpublicaccessblock import v1beta1 as pabv1beta1
from models.io.upbound.aws.s3.bucketversioning import v1beta1 as verv1beta1
from models.io.upbound.aws.s3.bucketserversideencryptionconfiguration import (
    v1beta1 as ssev1beta1,
)

class FunctionRunner(grpcv1.FunctionRunnerService):
    """A FunctionRunner handles gRPC RunFunctionRequests."""

    def __init__(self):
        """Create a new FunctionRunner."""
        self.log = logging.get_logger()

    async def RunFunction(
        self, req: fnv1.RunFunctionRequest, _: grpc.aio.ServicerContext
    ) -> fnv1.RunFunctionResponse:
        """Run the function."""
        log = self.log.bind(tag=req.meta.tag)
        log.info("Running function")

        rsp = response.to(req)

        observed_xr = v1alpha1.StorageBucket(**resource.struct_to_dict(req.observed.composite.resource))
        assert observed_xr is not None
        assert observed_xr.metadata is not None
        assert observed_xr.spec is not None

        spec = observed_xr.spec
        assert spec.region is not None
        
        desired_bucket = bucketv1beta1.Bucket(
            spec=bucketv1beta1.Spec(
                forProvider=bucketv1beta1.ForProvider(
                    region=spec.region,
                ),
            ),
        )
        resource.update(rsp.desired.resources["bucket"], desired_bucket)

        # Return early if Crossplane hasn't observed the bucket yet. This means it
        # hasn't been created yet. This function will be called again after it is.
        if "bucket" not in req.observed.resources:
            return rsp

        observed_bucket = bucketv1beta1.Bucket(**resource.struct_to_dict(req.observed.resources["bucket"].resource))

        # The desired encryption, public access block, and versioning resources all
        # need to refer to the bucket by its external name, which is stored in its
        # external name annotation. Return early if the Bucket's external-name
        # annotation isn't set yet.
        if observed_bucket.metadata is None or observed_bucket.metadata.annotations is None:
            return rsp
        if "crossplane.io/external-name" not in observed_bucket.metadata.annotations:
            return rsp

        bucket_external_name = observed_bucket.metadata.annotations[
            "crossplane.io/external-name"
        ]

        # When acl=public-read we still block public ACLs (modern buckets use
        # BucketOwnerEnforced) but loosen the policy-related blocks so our
        # BucketPolicy can grant anonymous read.
        is_public_read = spec.acl == "public-read"
        desired_pab = pabv1beta1.BucketPublicAccessBlock(
            spec=pabv1beta1.Spec(
                forProvider=pabv1beta1.ForProvider(
                    region=spec.region,
                    bucket=bucket_external_name,
                    blockPublicAcls=True,
                    ignorePublicAcls=True,
                    blockPublicPolicy=not is_public_read,
                    restrictPublicBuckets=not is_public_read,
                )
            ),
        )
        resource.update(rsp.desired.resources["pab"], desired_pab)

        if is_public_read:
            policy_doc = {
                "Version": "2012-10-17",
                "Statement": [{
                    "Sid": "PublicRead",
                    "Effect": "Allow",
                    "Principal": "*",
                    "Action": ["s3:GetObject"],
                    "Resource": [f"arn:aws:s3:::{bucket_external_name}/*"],
                }],
            }
            desired_policy = policyv1beta1.BucketPolicy(
                spec=policyv1beta1.Spec(
                    forProvider=policyv1beta1.ForProvider(
                        region=spec.region,
                        bucket=bucket_external_name,
                        policy=json.dumps(policy_doc),
                    ),
                ),
            )
            resource.update(rsp.desired.resources["policy"], desired_policy)

        desired_sse = ssev1beta1.BucketServerSideEncryptionConfiguration(
            spec=ssev1beta1.Spec(
                forProvider=ssev1beta1.ForProvider(
                    region=spec.region,
                    bucket=bucket_external_name,
                    rule=[
                        ssev1beta1.RuleItem(
                            applyServerSideEncryptionByDefault=[
                                ssev1beta1.ApplyServerSideEncryptionByDefaultItem(
                                    sseAlgorithm="AES256",
                                ),
                            ],
                            bucketKeyEnabled=True,
                        ),
                    ],
                ),
            ),
        )
        resource.update(rsp.desired.resources["sse"], desired_sse)

        # Return early without composing a BucketVersioning MR if the XR doesn't
        # have versioning enabled.
        if not spec.versioning:
            return rsp

        desired_versioning = verv1beta1.BucketVersioning(
            spec=verv1beta1.Spec(
                forProvider=verv1beta1.ForProvider(
                    region=spec.region,
                    bucket=bucket_external_name,
                    versioningConfiguration=[
                        verv1beta1.VersioningConfigurationItem(
                            status="Enabled",
                        ),
                    ],
                ),
            ),
        )
        resource.update(rsp.desired.resources["versioning"], desired_versioning)

        return rsp
