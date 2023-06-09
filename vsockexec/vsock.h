#ifndef VSOCK_H
#define VSOCK_H

#define VMADDR_CID_ANY -1U
#define VMADDR_CID_HOST 2

int openvsock(unsigned int cid, unsigned int port);
int listenacceptvsock(unsigned int cid, unsigned int port);

#endif
