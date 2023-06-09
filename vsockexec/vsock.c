#include "vsock.h"
#include <sys/socket.h>
#include <unistd.h>

struct sockaddr_vm {
	unsigned short svm_family;
	unsigned short svm_reserved1;
	unsigned int svm_port;
	unsigned int svm_cid;
	unsigned char svm_zero[sizeof(struct sockaddr) -
			       sizeof(sa_family_t) -
			       sizeof(unsigned short) -
			       sizeof(unsigned int) - sizeof(unsigned int)];
};

int openvsock(unsigned int cid, unsigned int port) {
    int s = socket(AF_VSOCK, SOCK_STREAM, 0);
    if (s < 0) {
        return -1;
    }

    struct sockaddr_vm addr = {0};
    addr.svm_family = AF_VSOCK;
    addr.svm_port = port;
    addr.svm_cid = cid;
    if (connect(s, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        return -1;
    }

    return s;
}

int listenacceptvsock(unsigned int cid, unsigned int port) {    
    int s = socket(AF_VSOCK, SOCK_STREAM, 0);
    int l;

    if (s < 0) {
        return -1;
    }

    struct sockaddr_vm addr = {0};
    addr.svm_family = AF_VSOCK;
    addr.svm_port = port;
    addr.svm_cid = cid;
    socklen_t len = sizeof(addr);
    if(bind(s, (struct sockaddr *)&addr, len)) {
        return -1;
    }
    if (listen(s, 1)) {
        return -1;
    }
    if ((l = accept(s, (struct sockaddr *)&addr, &len)) < 0) {
        return -1;
    }
    close(s);
    return l;
}