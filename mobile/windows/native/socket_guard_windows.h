/* SPDX-License-Identifier: GPL-2.0-or-later */
#ifndef QNTW_TINC_SOCKET_GUARD_H
#define QNTW_TINC_SOCKET_GUARD_H
#include <winsock2.h>
#include <ws2tcpip.h>
int qntw_guarded_socket(int family, int type, int protocol);
int qntw_guarded_connect(SOCKET fd, const struct sockaddr *address, int length);
int qntw_guarded_bind(SOCKET fd, const struct sockaddr *address, int length);
int qntw_guarded_accept(SOCKET fd, struct sockaddr *address, int *length);
int qntw_numeric_getaddrinfo(const char *node, const char *service, const struct addrinfo *hints, struct addrinfo **result);
int qntw_numeric_getnameinfo(const struct sockaddr *address, socklen_t length, char *host, socklen_t host_length, char *service, socklen_t service_length, int flags);
#ifndef QNTW_SOCKET_GUARD_IMPLEMENTATION
#define socket qntw_guarded_socket
#define connect qntw_guarded_connect
#define bind qntw_guarded_bind
#define accept qntw_guarded_accept
#define getaddrinfo qntw_numeric_getaddrinfo
#define getnameinfo qntw_numeric_getnameinfo
#endif
#endif
