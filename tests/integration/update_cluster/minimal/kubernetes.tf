locals {
  cluster_name                 = "minimal.example.com"
  master_autoscaling_group_ids = [aws_autoscaling_group.master-us-test-1a-masters-minimal-example-com.id]
  master_security_group_ids    = [aws_security_group.masters-minimal-example-com.id]
  masters_role_arn             = aws_iam_role.masters-minimal-example-com.arn
  masters_role_name            = aws_iam_role.masters-minimal-example-com.name
  node_autoscaling_group_ids   = [aws_autoscaling_group.nodes-minimal-example-com.id]
  node_security_group_ids      = [aws_security_group.nodes-minimal-example-com.id]
  node_subnet_ids              = [aws_subnet.us-test-1a-minimal-example-com.id]
  nodes_role_arn               = aws_iam_role.nodes-minimal-example-com.arn
  nodes_role_name              = aws_iam_role.nodes-minimal-example-com.name
  region                       = "us-test-1"
  route_table_public_id        = aws_route_table.minimal-example-com.id
  subnet_us-test-1a_id         = aws_subnet.us-test-1a-minimal-example-com.id
  vpc_cidr_block               = aws_vpc.minimal-example-com.cidr_block
  vpc_id                       = aws_vpc.minimal-example-com.id
}

output "cluster_name" {
  value = "minimal.example.com"
}

output "master_autoscaling_group_ids" {
  value = [aws_autoscaling_group.master-us-test-1a-masters-minimal-example-com.id]
}

output "master_security_group_ids" {
  value = [aws_security_group.masters-minimal-example-com.id]
}

output "masters_role_arn" {
  value = aws_iam_role.masters-minimal-example-com.arn
}

output "masters_role_name" {
  value = aws_iam_role.masters-minimal-example-com.name
}

output "node_autoscaling_group_ids" {
  value = [aws_autoscaling_group.nodes-minimal-example-com.id]
}

output "node_security_group_ids" {
  value = [aws_security_group.nodes-minimal-example-com.id]
}

output "node_subnet_ids" {
  value = [aws_subnet.us-test-1a-minimal-example-com.id]
}

output "nodes_role_arn" {
  value = aws_iam_role.nodes-minimal-example-com.arn
}

output "nodes_role_name" {
  value = aws_iam_role.nodes-minimal-example-com.name
}

output "region" {
  value = "us-test-1"
}

output "route_table_public_id" {
  value = aws_route_table.minimal-example-com.id
}

output "subnet_us-test-1a_id" {
  value = aws_subnet.us-test-1a-minimal-example-com.id
}

output "vpc_cidr_block" {
  value = aws_vpc.minimal-example-com.cidr_block
}

output "vpc_id" {
  value = aws_vpc.minimal-example-com.id
}

provider "aws" {
  region = "us-test-1"
}

resource "aws_autoscaling_group" "master-us-test-1a-masters-minimal-example-com" {
  enabled_metrics = ["GroupDesiredCapacity", "GroupInServiceInstances", "GroupMaxSize", "GroupMinSize", "GroupPendingInstances", "GroupStandbyInstances", "GroupTerminatingInstances", "GroupTotalInstances"]
  launch_template {
    id      = aws_launch_template.master-us-test-1a-masters-minimal-example-com.id
    version = aws_launch_template.master-us-test-1a-masters-minimal-example-com.latest_version
  }
  max_size            = 1
  metrics_granularity = "1Minute"
  min_size            = 1
  name                = "master-us-test-1a.masters.minimal.example.com"
  tag {
    key                 = "KubernetesCluster"
    propagate_at_launch = true
    value               = "minimal.example.com"
  }
  tag {
    key                 = "Name"
    propagate_at_launch = true
    value               = "master-us-test-1a.masters.minimal.example.com"
  }
  tag {
    key                 = "k8s.io/role/master"
    propagate_at_launch = true
    value               = "1"
  }
  tag {
    key                 = "kops.k8s.io/instancegroup"
    propagate_at_launch = true
    value               = "master-us-test-1a"
  }
  tag {
    key                 = "kubernetes.io/cluster/minimal.example.com"
    propagate_at_launch = true
    value               = "owned"
  }
  vpc_zone_identifier = [aws_subnet.us-test-1a-minimal-example-com.id]
}

resource "aws_autoscaling_group" "nodes-minimal-example-com" {
  enabled_metrics = ["GroupDesiredCapacity", "GroupInServiceInstances", "GroupMaxSize", "GroupMinSize", "GroupPendingInstances", "GroupStandbyInstances", "GroupTerminatingInstances", "GroupTotalInstances"]
  launch_template {
    id      = aws_launch_template.nodes-minimal-example-com.id
    version = aws_launch_template.nodes-minimal-example-com.latest_version
  }
  max_size            = 2
  metrics_granularity = "1Minute"
  min_size            = 2
  name                = "nodes.minimal.example.com"
  tag {
    key                 = "KubernetesCluster"
    propagate_at_launch = true
    value               = "minimal.example.com"
  }
  tag {
    key                 = "Name"
    propagate_at_launch = true
    value               = "nodes.minimal.example.com"
  }
  tag {
    key                 = "k8s.io/role/node"
    propagate_at_launch = true
    value               = "1"
  }
  tag {
    key                 = "kops.k8s.io/instancegroup"
    propagate_at_launch = true
    value               = "nodes"
  }
  tag {
    key                 = "kubernetes.io/cluster/minimal.example.com"
    propagate_at_launch = true
    value               = "owned"
  }
  vpc_zone_identifier = [aws_subnet.us-test-1a-minimal-example-com.id]
}

resource "aws_ebs_volume" "us-test-1a-etcd-events-minimal-example-com" {
  availability_zone = "us-test-1a"
  encrypted         = false
  size              = 20
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "us-test-1a.etcd-events.minimal.example.com"
    "k8s.io/etcd/events"                        = "us-test-1a/us-test-1a"
    "k8s.io/role/master"                        = "1"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  type = "gp2"
}

resource "aws_ebs_volume" "us-test-1a-etcd-main-minimal-example-com" {
  availability_zone = "us-test-1a"
  encrypted         = false
  size              = 20
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "us-test-1a.etcd-main.minimal.example.com"
    "k8s.io/etcd/main"                          = "us-test-1a/us-test-1a"
    "k8s.io/role/master"                        = "1"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  type = "gp2"
}

resource "aws_iam_instance_profile" "masters-minimal-example-com" {
  name = "masters.minimal.example.com"
  role = aws_iam_role.masters-minimal-example-com.name
}

resource "aws_iam_instance_profile" "nodes-minimal-example-com" {
  name = "nodes.minimal.example.com"
  role = aws_iam_role.nodes-minimal-example-com.name
}

resource "aws_iam_role_policy" "masters-minimal-example-com" {
  name   = "masters.minimal.example.com"
  policy = file("${path.module}/data/aws_iam_role_policy_masters.minimal.example.com_policy")
  role   = aws_iam_role.masters-minimal-example-com.name
}

resource "aws_iam_role_policy" "nodes-minimal-example-com" {
  name   = "nodes.minimal.example.com"
  policy = file("${path.module}/data/aws_iam_role_policy_nodes.minimal.example.com_policy")
  role   = aws_iam_role.nodes-minimal-example-com.name
}

resource "aws_iam_role" "masters-minimal-example-com" {
  assume_role_policy = file("${path.module}/data/aws_iam_role_masters.minimal.example.com_policy")
  name               = "masters.minimal.example.com"
}

resource "aws_iam_role" "nodes-minimal-example-com" {
  assume_role_policy = file("${path.module}/data/aws_iam_role_nodes.minimal.example.com_policy")
  name               = "nodes.minimal.example.com"
}

resource "aws_internet_gateway" "minimal-example-com" {
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  vpc_id = aws_vpc.minimal-example-com.id
}

resource "aws_key_pair" "kubernetes-minimal-example-com-c4a6ed9aa889b9e2c39cd663eb9c7157" {
  key_name   = "kubernetes.minimal.example.com-c4:a6:ed:9a:a8:89:b9:e2:c3:9c:d6:63:eb:9c:71:57"
  public_key = file("${path.module}/data/aws_key_pair_kubernetes.minimal.example.com-c4a6ed9aa889b9e2c39cd663eb9c7157_public_key")
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
}

resource "aws_launch_template" "master-us-test-1a-masters-minimal-example-com" {
  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      delete_on_termination = true
      volume_size           = 64
      volume_type           = "gp2"
    }
  }
  block_device_mappings {
    device_name  = "/dev/sdc"
    virtual_name = "ephemeral0"
  }
  iam_instance_profile {
    name = aws_iam_instance_profile.masters-minimal-example-com.id
  }
  image_id      = "ami-12345678"
  instance_type = "m3.medium"
  key_name      = aws_key_pair.kubernetes-minimal-example-com-c4a6ed9aa889b9e2c39cd663eb9c7157.id
  lifecycle {
    create_before_destroy = true
  }
  name_prefix = "master-us-test-1a.masters.minimal.example.com-"
  network_interfaces {
    associate_public_ip_address = true
    delete_on_termination       = true
    security_groups             = [aws_security_group.masters-minimal-example-com.id]
  }
  tag_specifications {
    resource_type = "instance"
    tags = {
      "KubernetesCluster"                         = "minimal.example.com"
      "Name"                                      = "master-us-test-1a.masters.minimal.example.com"
      "k8s.io/role/master"                        = "1"
      "kops.k8s.io/instancegroup"                 = "master-us-test-1a"
      "kubernetes.io/cluster/minimal.example.com" = "owned"
    }
  }
  tag_specifications {
    resource_type = "volume"
    tags = {
      "KubernetesCluster"                         = "minimal.example.com"
      "Name"                                      = "master-us-test-1a.masters.minimal.example.com"
      "k8s.io/role/master"                        = "1"
      "kops.k8s.io/instancegroup"                 = "master-us-test-1a"
      "kubernetes.io/cluster/minimal.example.com" = "owned"
    }
  }
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "master-us-test-1a.masters.minimal.example.com"
    "k8s.io/role/master"                        = "1"
    "kops.k8s.io/instancegroup"                 = "master-us-test-1a"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  user_data = filebase64("${path.module}/data/aws_launch_template_master-us-test-1a.masters.minimal.example.com_user_data")
}

resource "aws_launch_template" "nodes-minimal-example-com" {
  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      delete_on_termination = true
      volume_size           = 128
      volume_type           = "gp2"
    }
  }
  iam_instance_profile {
    name = aws_iam_instance_profile.nodes-minimal-example-com.id
  }
  image_id      = "ami-12345678"
  instance_type = "t2.medium"
  key_name      = aws_key_pair.kubernetes-minimal-example-com-c4a6ed9aa889b9e2c39cd663eb9c7157.id
  lifecycle {
    create_before_destroy = true
  }
  name_prefix = "nodes.minimal.example.com-"
  network_interfaces {
    associate_public_ip_address = true
    delete_on_termination       = true
    security_groups             = [aws_security_group.nodes-minimal-example-com.id]
  }
  tag_specifications {
    resource_type = "instance"
    tags = {
      "KubernetesCluster"                         = "minimal.example.com"
      "Name"                                      = "nodes.minimal.example.com"
      "k8s.io/role/node"                          = "1"
      "kops.k8s.io/instancegroup"                 = "nodes"
      "kubernetes.io/cluster/minimal.example.com" = "owned"
    }
  }
  tag_specifications {
    resource_type = "volume"
    tags = {
      "KubernetesCluster"                         = "minimal.example.com"
      "Name"                                      = "nodes.minimal.example.com"
      "k8s.io/role/node"                          = "1"
      "kops.k8s.io/instancegroup"                 = "nodes"
      "kubernetes.io/cluster/minimal.example.com" = "owned"
    }
  }
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "nodes.minimal.example.com"
    "k8s.io/role/node"                          = "1"
    "kops.k8s.io/instancegroup"                 = "nodes"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  user_data = filebase64("${path.module}/data/aws_launch_template_nodes.minimal.example.com_user_data")
}

resource "aws_route_table_association" "us-test-1a-minimal-example-com" {
  route_table_id = aws_route_table.minimal-example-com.id
  subnet_id      = aws_subnet.us-test-1a-minimal-example-com.id
}

resource "aws_route_table" "minimal-example-com" {
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
    "kubernetes.io/kops/role"                   = "public"
  }
  vpc_id = aws_vpc.minimal-example-com.id
}

resource "aws_route" "route-0-0-0-0--0" {
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.minimal-example-com.id
  route_table_id         = aws_route_table.minimal-example-com.id
}

resource "aws_security_group_rule" "all-master-to-master" {
  from_port                = 0
  protocol                 = "-1"
  security_group_id        = aws_security_group.masters-minimal-example-com.id
  source_security_group_id = aws_security_group.masters-minimal-example-com.id
  to_port                  = 0
  type                     = "ingress"
}

resource "aws_security_group_rule" "all-master-to-node" {
  from_port                = 0
  protocol                 = "-1"
  security_group_id        = aws_security_group.nodes-minimal-example-com.id
  source_security_group_id = aws_security_group.masters-minimal-example-com.id
  to_port                  = 0
  type                     = "ingress"
}

resource "aws_security_group_rule" "all-node-to-node" {
  from_port                = 0
  protocol                 = "-1"
  security_group_id        = aws_security_group.nodes-minimal-example-com.id
  source_security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port                  = 0
  type                     = "ingress"
}

resource "aws_security_group_rule" "https-external-to-master-0-0-0-0--0" {
  cidr_blocks       = ["0.0.0.0/0"]
  from_port         = 443
  protocol          = "tcp"
  security_group_id = aws_security_group.masters-minimal-example-com.id
  to_port           = 443
  type              = "ingress"
}

resource "aws_security_group_rule" "master-egress" {
  cidr_blocks       = ["0.0.0.0/0"]
  from_port         = 0
  protocol          = "-1"
  security_group_id = aws_security_group.masters-minimal-example-com.id
  to_port           = 0
  type              = "egress"
}

resource "aws_security_group_rule" "node-egress" {
  cidr_blocks       = ["0.0.0.0/0"]
  from_port         = 0
  protocol          = "-1"
  security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port           = 0
  type              = "egress"
}

resource "aws_security_group_rule" "node-to-master-tcp-1-2379" {
  from_port                = 1
  protocol                 = "tcp"
  security_group_id        = aws_security_group.masters-minimal-example-com.id
  source_security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port                  = 2379
  type                     = "ingress"
}

resource "aws_security_group_rule" "node-to-master-tcp-2382-4000" {
  from_port                = 2382
  protocol                 = "tcp"
  security_group_id        = aws_security_group.masters-minimal-example-com.id
  source_security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port                  = 4000
  type                     = "ingress"
}

resource "aws_security_group_rule" "node-to-master-tcp-4003-65535" {
  from_port                = 4003
  protocol                 = "tcp"
  security_group_id        = aws_security_group.masters-minimal-example-com.id
  source_security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port                  = 65535
  type                     = "ingress"
}

resource "aws_security_group_rule" "node-to-master-udp-1-65535" {
  from_port                = 1
  protocol                 = "udp"
  security_group_id        = aws_security_group.masters-minimal-example-com.id
  source_security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port                  = 65535
  type                     = "ingress"
}

resource "aws_security_group_rule" "ssh-external-to-master-0-0-0-0--0" {
  cidr_blocks       = ["0.0.0.0/0"]
  from_port         = 22
  protocol          = "tcp"
  security_group_id = aws_security_group.masters-minimal-example-com.id
  to_port           = 22
  type              = "ingress"
}

resource "aws_security_group_rule" "ssh-external-to-node-0-0-0-0--0" {
  cidr_blocks       = ["0.0.0.0/0"]
  from_port         = 22
  protocol          = "tcp"
  security_group_id = aws_security_group.nodes-minimal-example-com.id
  to_port           = 22
  type              = "ingress"
}

resource "aws_security_group" "masters-minimal-example-com" {
  description = "Security group for masters"
  name        = "masters.minimal.example.com"
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "masters.minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  vpc_id = aws_vpc.minimal-example-com.id
}

resource "aws_security_group" "nodes-minimal-example-com" {
  description = "Security group for nodes"
  name        = "nodes.minimal.example.com"
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "nodes.minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
  vpc_id = aws_vpc.minimal-example-com.id
}

resource "aws_subnet" "us-test-1a-minimal-example-com" {
  availability_zone = "us-test-1a"
  cidr_block        = "172.20.32.0/19"
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "us-test-1a.minimal.example.com"
    "SubnetType"                                = "Public"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
    "kubernetes.io/role/elb"                    = "1"
  }
  vpc_id = aws_vpc.minimal-example-com.id
}

resource "aws_vpc_dhcp_options_association" "minimal-example-com" {
  dhcp_options_id = aws_vpc_dhcp_options.minimal-example-com.id
  vpc_id          = aws_vpc.minimal-example-com.id
}

resource "aws_vpc_dhcp_options" "minimal-example-com" {
  domain_name         = "us-test-1.compute.internal"
  domain_name_servers = ["AmazonProvidedDNS"]
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
}

resource "aws_vpc" "minimal-example-com" {
  cidr_block           = "172.20.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags = {
    "KubernetesCluster"                         = "minimal.example.com"
    "Name"                                      = "minimal.example.com"
    "kubernetes.io/cluster/minimal.example.com" = "owned"
  }
}

terraform {
  required_version = ">= 0.12.0"
}
