resource "nat-address" {
	volume 0 {
		device minor 99;
		disk "/dev/foo/bar4";
		meta-disk "internal";
	}
	on "undertest" {
		node-id 0;
	}
	on "other" {
		node-id 1;
	}
	connection {
	}
}
