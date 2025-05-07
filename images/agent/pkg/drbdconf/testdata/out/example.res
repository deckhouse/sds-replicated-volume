include "/var/lib/linstor.d/*.res";

resource r0 {
	net {
		protocol C;
		cram-hmac-alg sha1;
		shared-secret "FooFunFactory";
	}
	disk {
		resync-rate 10M;
	}
	on alice {
		volume 0 {
			device minor 1;
			disk /dev/sda7;
			meta-disk internal;
		}
		address 10.1.1.31:7789;
	}
	on bob {
		volume 0 {
			device minor 1;
			disk /dev/sda7;
			meta-disk internal;
		}
		address 10.1.1.32:7789;
	}
}

skip resource "pvc-65bee3d7-ae9a-435c-980f-1c84c7621d27" {
	options {
		on-no-data-accessible suspend-io;
		on-no-quorum suspend-io;
		on-suspended-primary-outdated force-secondary;
		quorum majority;
		quorum-minimum-redundancy 2;
	}
	net {
		cram-hmac-alg sha1;
		shared-secret "fvdXdAsLg5aWzOepD0SO";
		protocol C;
		rr-conflict retry-connect;
		verify-alg "crct10dif-pclmul";
	}
	on "a-stefurishin-worker-0" {
		volume 0 {
			disk /dev/vg-0/pvc-65bee3d7-ae9a-435c-980f-1c84c7621d27_00000;
			disk {
				discard-zeroes-if-aligned no;
			}
			meta-disk internal;
			device minor 1000;
		}
		node-id 0;
	}
	on "a-stefurishin-worker-1" {
		volume 0 {
			disk /dev/drbd/this/is/not/used;
			disk {
				discard-zeroes-if-aligned no;
			}
			meta-disk internal;
			device minor 1000;
		}
		node-id 1;
	}
	on "a-stefurishin-worker-2" {
		volume 0 {
			disk /dev/drbd/this/is/not/used;
			disk {
				discard-zeroes-if-aligned no;
			}
			meta-disk internal;
			device minor 1000;
		}
		node-id 2;
	}
	connection {
		host "a-stefurishin-worker-0" address 10.10.11.52:7000;
		host "a-stefurishin-worker-1" address ipv4 10.10.11.149:7000;
	}
	connection {
		host "a-stefurishin-worker-0" address ipv4 10.10.11.52:7000;
		host "a-stefurishin-worker-2" address ipv4 10.10.11.150:7000;
	}
}

