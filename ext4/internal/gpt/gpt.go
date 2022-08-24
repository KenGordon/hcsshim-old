package gpt

// TODO katiewasnothere:
// need the ability to write a block
// need the ability to write zeros

// I don't know the starting or ending partition until after we've written it to disk
// So maybe we can make a partition for each layer given and not write them until after
// we've written the ext4s. So we'd need to seek into the disk based on the size of the
// entry array that we think we're gonna make.

// Do I really need the partition name?
