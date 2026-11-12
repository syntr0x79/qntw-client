/* SPDX-License-Identifier: GPL-2.0-or-later
 * Fail-closed bridge to the app's physical-network socket preparer. */
#define _POSIX_C_SOURCE 200809L
#ifndef _DARWIN_C_SOURCE
#define _DARWIN_C_SOURCE
#endif
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#define QNTW_SOCKET_GUARD_IMPLEMENTATION
#include "socket_guard.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/un.h>
#include <time.h>
#include <poll.h>
#include <unistd.h>

#define GUARD_TIMEOUT_MS 5000

static int64_t monotonic_ms(void) {
    struct timespec now;
    if(clock_gettime(CLOCK_MONOTONIC, &now) != 0) return -1;
    return (int64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000;
}

static int wait_ready(int fd, short events, int64_t deadline) {
    for(;;) {
        int64_t now = monotonic_ms();
        if(now < 0) return -1;
        int64_t remaining = deadline - now;
        if(remaining <= 0) { errno = ETIMEDOUT; return -1; }
        struct pollfd descriptor = {.fd = fd, .events = events};
        int result = poll(&descriptor, 1, remaining > INT_MAX ? INT_MAX : (int)remaining);
        if(result < 0 && errno == EINTR) continue;
        if(result <= 0) { if(!result) errno = ETIMEDOUT; return -1; }
        if(descriptor.revents & events) return 0;
        errno = EIO;
        return -1;
    }
}

static int prepare_socket(int fd) {
    const char *path = getenv("TINC_SOCKET_GUARD");
    if(!path || !*path) { errno = EPERM; return -1; }

    struct sockaddr_un address = {0};
    address.sun_family = AF_UNIX;
    size_t path_length = strlen(path);
    if(path_length >= sizeof(address.sun_path) || path[0] != '/') {
        errno = EINVAL;
        return -1;
    }
    memcpy(address.sun_path, path, path_length + 1);
    int64_t now = monotonic_ms();
    if(now < 0) return -1;
    int64_t deadline = now + GUARD_TIMEOUT_MS;
    int control = socket(AF_UNIX, SOCK_STREAM, 0);
    if(control < 0) return -1;
    if(fcntl(control, F_SETFD, FD_CLOEXEC) < 0 ||
       fcntl(control, F_SETFL, O_NONBLOCK) < 0) goto fail;
#ifdef SO_NOSIGPIPE
    int no_sigpipe = 1;
    if(setsockopt(control, SOL_SOCKET, SO_NOSIGPIPE, &no_sigpipe, sizeof(no_sigpipe)) < 0) goto fail;
#endif
    socklen_t length = (socklen_t)(offsetof(struct sockaddr_un, sun_path) + path_length + 1);
    if(connect(control, (struct sockaddr *)&address, length) < 0) {
        if(errno != EINPROGRESS && errno != EAGAIN) goto fail;
        if(wait_ready(control, POLLOUT, deadline) < 0) goto fail;
        int error = 0;
        socklen_t error_length = sizeof(error);
        if(getsockopt(control, SOL_SOCKET, SO_ERROR, &error, &error_length) < 0) goto fail;
        if(error) { errno = error; goto fail; }
    }

    uint8_t version = 1;
    union {
        struct cmsghdr alignment;
        unsigned char bytes[CMSG_SPACE(sizeof(int))];
    } ancillary = {0};
    struct iovec vector = {.iov_base = &version, .iov_len = 1};
    struct msghdr message = {0};
    message.msg_iov = &vector;
    message.msg_iovlen = 1;
    message.msg_control = ancillary.bytes;
    message.msg_controllen = sizeof(ancillary.bytes);
    struct cmsghdr *header = CMSG_FIRSTHDR(&message);
    header->cmsg_level = SOL_SOCKET;
    header->cmsg_type = SCM_RIGHTS;
    header->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(header), &fd, sizeof(fd));
    int flags = 0;
#ifdef MSG_NOSIGNAL
    flags = MSG_NOSIGNAL;
#endif
    for(;;) {
        ssize_t sent = sendmsg(control, &message, flags);
        if(sent == 1) break;
        if(sent < 0 && errno == EINTR) continue;
        if(sent < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            if(wait_ready(control, POLLOUT, deadline) == 0) continue;
        }
        goto fail;
    }
    for(;;) {
        uint8_t response = 255;
        ssize_t received = read(control, &response, 1);
        if(received == 1) {
            if(response != 0) { errno = EPERM; goto fail; }
            close(control);
            return 0;
        }
        if(received < 0 && errno == EINTR) continue;
        if(received < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            if(wait_ready(control, POLLIN, deadline) == 0) continue;
        }
        if(received == 0) errno = EPERM;
        goto fail;
    }
fail:;
    int error = errno;
    close(control);
    errno = error;
    return -1;
}

int qntw_guarded_socket(int family, int type, int protocol) {
    /* Other device backends are unnecessary in the client build. Rejecting
     * their socket families also prevents an unguarded raw packet path. */
    if(family != AF_INET && family != AF_INET6 && family != AF_UNIX) {
        errno = EAFNOSUPPORT;
        return -1;
    }
    int fd = socket(family, type, protocol);
    if(fd < 0 || family == AF_UNIX) return fd;
    if(prepare_socket(fd) == 0) return fd;
    int error = errno;
    close(fd);
    errno = error;
    return -1;
}

int qntw_numeric_getaddrinfo(const char *node, const char *service,
                            const struct addrinfo *hints, struct addrinfo **result) {
    struct addrinfo numeric = {0};
    if(hints) numeric = *hints;
    numeric.ai_flags |= AI_NUMERICHOST | AI_NUMERICSERV;
    return getaddrinfo(node, service, &numeric, result);
}

int qntw_numeric_getnameinfo(const struct sockaddr *address, socklen_t length,
                           char *host, socklen_t host_length,
                           char *service, socklen_t service_length, int flags) {
    flags &= ~NI_NAMEREQD;
    return getnameinfo(address, length, host, host_length, service, service_length,
                       flags | NI_NUMERICHOST | NI_NUMERICSERV);
}
