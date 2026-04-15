/*
 * reproduce_double_reply.c — Reproduces the kernel 6.8 NBD "Double reply" bug.
 *
 * Root cause: when sock_sendmsg is interrupted (ERESTARTSYS) during WRITE
 * data-page send AFTER the header is on the socket, nbd_send_cmd returns
 * BLK_STS_RESOURCE. blk-mq frees the tag (blk_mq_put_driver_tag). The tag
 * is reused by a new request (cookie incremented). The old response arrives
 * with a stale cookie → "Double reply". Fixed in kernel 6.14.
 *
 * This program:
 *   1. Creates a socketpair with tiny buffers
 *   2. Connects an NBD device via ioctl (simpler than netlink)
 *   3. Spawns a slow NBD server thread (delays responses by 3s)
 *   4. Spawns a signal thread that sends SIGALRM (without SA_RESTART) to
 *      the writer thread, interrupting sock_sendmsg → ERESTARTSYS
 *   5. Issues concurrent writes to the NBD device
 *   6. Checks dmesg for "Double reply"
 *
 * Build: gcc -O2 -pthread -o reproduce_double_reply reproduce_double_reply.c
 * Run:   sudo ./reproduce_double_reply
 * Check: dmesg | grep -i "double reply\|unexpected reply"
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <pthread.h>
#include <sys/socket.h>
#include <sys/ioctl.h>
#include <sys/un.h>
#include <linux/nbd.h>
#include <arpa/inet.h>
#include <stdint.h>
#include <time.h>

#define NBD_DEVICE  "/dev/nbd15"
#define DEVICE_SIZE (64ULL * 1024 * 1024)  /* 64 MB */
#define BLOCK_SIZE  4096
#define WRITE_SIZE  (128 * 1024)  /* 128KB per write */
#define NUM_WRITERS 8
#define TEST_DURATION_SEC 15
#define SLOW_DELAY_MS 3000  /* 3 second delay for reads */

/* NBD protocol constants */
#define NBD_REQUEST_MAGIC  0x25609513
#define NBD_REPLY_MAGIC    0x67446698

struct nbd_request_pkt {
    uint32_t magic;
    uint32_t type;
    uint64_t handle;
    uint64_t from;
    uint32_t len;
} __attribute__((packed));

struct nbd_reply_pkt {
    uint32_t magic;
    uint32_t error;
    uint64_t handle;
} __attribute__((packed));

static volatile int g_stop = 0;
static int g_nbd_fd = -1;
static int g_server_fd = -1;
static pthread_t g_writer_tids[NUM_WRITERS];

/* Signal handler — intentionally empty. The point is to interrupt
 * sock_sendmsg without SA_RESTART so ERESTARTSYS propagates. */
static void sigalrm_handler(int sig) {
    (void)sig;
}

/* NBD server thread: reads requests, delays, responds */
static void *nbd_server_thread(void *arg) {
    int fd = *(int *)arg;
    unsigned char buf[4096];
    struct nbd_request_pkt req;
    struct nbd_reply_pkt reply;

    while (!g_stop) {
        /* Read request header (28 bytes) */
        ssize_t n = 0;
        while (n < (ssize_t)sizeof(req)) {
            ssize_t r = read(fd, ((char *)&req) + n, sizeof(req) - n);
            if (r <= 0) {
                if (r == 0 || (errno != EINTR && errno != EAGAIN))
                    goto out;
                continue;
            }
            n += r;
        }

        uint32_t magic = ntohl(req.magic);
        uint32_t type = ntohl(req.type);
        uint32_t len = ntohl(req.len);
        uint64_t handle = req.handle; /* network byte order, echo as-is */

        if (magic != NBD_REQUEST_MAGIC) {
            fprintf(stderr, "server: bad magic 0x%x\n", magic);
            break;
        }

        /* For disconnect, exit */
        if (type == 2) /* NBD_CMD_DISC */
            break;

        /* For writes, consume the data payload */
        if (type == 1) { /* NBD_CMD_WRITE */
            uint32_t remaining = len;
            while (remaining > 0) {
                uint32_t chunk = remaining < sizeof(buf) ? remaining : sizeof(buf);
                ssize_t r = read(fd, buf, chunk);
                if (r <= 0) {
                    if (r == 0 || (errno != EINTR && errno != EAGAIN))
                        goto out;
                    continue;
                }
                remaining -= r;
            }
        }

        /* Delay for reads (simulates slow GCS backend) */
        if (type == 0) { /* NBD_CMD_READ */
            struct timespec ts = { .tv_sec = SLOW_DELAY_MS / 1000,
                                   .tv_nsec = (SLOW_DELAY_MS % 1000) * 1000000L };
            nanosleep(&ts, NULL);
        }

        /* Send reply */
        memset(&reply, 0, sizeof(reply));
        reply.magic = htonl(NBD_REPLY_MAGIC);
        reply.error = 0;
        reply.handle = handle;

        ssize_t w = 0;
        while (w < (ssize_t)sizeof(reply)) {
            ssize_t s = write(fd, ((char *)&reply) + w, sizeof(reply) - w);
            if (s <= 0) {
                if (errno == EINTR || errno == EAGAIN)
                    continue;
                goto out;
            }
            w += s;
        }

        /* For read responses, send zero data */
        if (type == 0) {
            memset(buf, 0, sizeof(buf));
            uint32_t remaining = len;
            while (remaining > 0) {
                uint32_t chunk = remaining < sizeof(buf) ? remaining : sizeof(buf);
                ssize_t s = write(fd, buf, chunk);
                if (s <= 0) {
                    if (errno == EINTR || errno == EAGAIN)
                        continue;
                    goto out;
                }
                remaining -= s;
            }
        }
    }
out:
    return NULL;
}

/* Writer thread: issues writes to the NBD device */
static void *writer_thread(void *arg) {
    int id = *(int *)arg;
    char *buf = calloc(1, WRITE_SIZE);
    if (!buf) return NULL;
    memset(buf, (char)id, WRITE_SIZE);

    int fd = open(NBD_DEVICE, O_RDWR | O_SYNC);
    if (fd < 0) {
        perror("writer: open");
        free(buf);
        return NULL;
    }

	unsigned long count = 0;
	unsigned long eintr_count = 0;
	while (!g_stop) {
		off_t off = ((off_t)id * WRITE_SIZE + (count % 256) * WRITE_SIZE) % (DEVICE_SIZE - WRITE_SIZE);
		off = (off / BLOCK_SIZE) * BLOCK_SIZE;
		ssize_t n = pwrite(fd, buf, WRITE_SIZE, off);
		if (n < 0) {
			if (errno == EINTR) { eintr_count++; continue; }
			if (errno == EIO) {
				fprintf(stderr, "writer %d: EIO after %lu writes — device dead (bug triggered?)\n", id, count);
				break;
			}
			fprintf(stderr, "writer %d: pwrite error: %s (after %lu writes)\n", id, strerror(errno), count);
			break;
		}
		count++;
	}

	close(fd);
	free(buf);
	printf("writer %d: %lu writes, %lu EINTR\n", id, count, eintr_count);
	return NULL;
}

/* Signal bombardment thread: sends SIGALRM to writer threads */
static void *signal_thread(void *arg) {
    (void)arg;

    /* Wait a bit for writers to start */
    usleep(500000);

    while (!g_stop) {
        for (int i = 0; i < NUM_WRITERS; i++) {
            if (g_writer_tids[i])
                pthread_kill(g_writer_tids[i], SIGALRM);
        }
        usleep(50);  /* ~20000 signals/sec per writer */
    }
    return NULL;
}

/* NBD_DO_IT thread — blocks until disconnect */
static void *nbd_do_it_thread(void *arg) {
    int fd = *(int *)arg;
    int err = ioctl(fd, NBD_DO_IT);
    if (err < 0 && errno != EBUSY)
        fprintf(stderr, "NBD_DO_IT returned: %s\n", strerror(errno));
    return NULL;
}

int main(void) {
    int sv[2]; /* socketpair */
    int nbd_fd;
    int ret = 0;

    if (geteuid() != 0) {
        fprintf(stderr, "Must run as root\n");
        return 1;
    }

    /* Install SIGALRM WITHOUT SA_RESTART so ERESTARTSYS propagates */
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = sigalrm_handler;
    sa.sa_flags = 0;  /* NO SA_RESTART! */
    sigaction(SIGALRM, &sa, NULL);

    /* Create socketpair with tiny buffers */
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) < 0) {
        perror("socketpair");
        return 1;
    }

    /* Set minimum socket buffer size to force sock_sendmsg to block */
    int bufsize = 4096;
    setsockopt(sv[0], SOL_SOCKET, SO_SNDBUF, &bufsize, sizeof(bufsize));
    setsockopt(sv[0], SOL_SOCKET, SO_RCVBUF, &bufsize, sizeof(bufsize));
    setsockopt(sv[1], SOL_SOCKET, SO_SNDBUF, &bufsize, sizeof(bufsize));
    setsockopt(sv[1], SOL_SOCKET, SO_RCVBUF, &bufsize, sizeof(bufsize));

    /* Open NBD device */
    nbd_fd = open(NBD_DEVICE, O_RDWR);
    if (nbd_fd < 0) {
        perror("open nbd");
        ret = 1;
        goto cleanup_socks;
    }
    g_nbd_fd = nbd_fd;

    /* Configure NBD via ioctl */
	if (ioctl(nbd_fd, NBD_SET_SOCK, sv[0]) < 0) {
		perror("NBD_SET_SOCK");
		ret = 1;
		goto cleanup_nbd;
	}

    if (ioctl(nbd_fd, NBD_SET_BLKSIZE, (unsigned long)BLOCK_SIZE) < 0) {
        perror("NBD_SET_BLKSIZE");
        ret = 1;
        goto cleanup_nbd;
    }
    if (ioctl(nbd_fd, NBD_SET_SIZE_BLOCKS, (unsigned long)(DEVICE_SIZE / BLOCK_SIZE)) < 0) {
        perror("NBD_SET_SIZE_BLOCKS");
        ret = 1;
        goto cleanup_nbd;
    }
    if (ioctl(nbd_fd, NBD_SET_TIMEOUT, 90) < 0) {
        perror("NBD_SET_TIMEOUT");
        /* non-fatal */
    }

    /* Start NBD server thread */
    g_server_fd = sv[1];
    pthread_t server_tid;
    if (pthread_create(&server_tid, NULL, nbd_server_thread, &sv[1]) != 0) {
        perror("pthread_create server");
        ret = 1;
        goto cleanup_nbd;
    }

	/* Start NBD_DO_IT in a thread (this blocks until disconnect).
	 * NBD_DO_IT is the main I/O loop — it reads responses from the socket
	 * and completes requests. Must run in a separate thread. */
	pthread_t doit_tid;
	pthread_create(&doit_tid, NULL, nbd_do_it_thread, &nbd_fd);

	/* Give the device time to become ready and the partition scan to settle */
	sleep(2);

    /* Start writer threads */
    int writer_ids[NUM_WRITERS];
    for (int i = 0; i < NUM_WRITERS; i++) {
        writer_ids[i] = i;
        pthread_create(&g_writer_tids[i], NULL, writer_thread, &writer_ids[i]);
    }

    /* Start signal bombardment thread */
    pthread_t sig_tid;
    pthread_create(&sig_tid, NULL, signal_thread, NULL);

    /* Run for TEST_DURATION_SEC seconds */
    printf("Running for %d seconds with %d writers on %s...\n",
           TEST_DURATION_SEC, NUM_WRITERS, NBD_DEVICE);
    printf("Signals: SIGALRM (no SA_RESTART) to writer threads\n");
    printf("Socket buffers: %d bytes\n", bufsize);
    sleep(TEST_DURATION_SEC);

    /* Stop everything */
    g_stop = 1;
    printf("Stopping...\n");

    /* Wait for writers */
    for (int i = 0; i < NUM_WRITERS; i++) {
        if (g_writer_tids[i])
            pthread_join(g_writer_tids[i], NULL);
    }
    pthread_join(sig_tid, NULL);

	/* Disconnect NBD */
	printf("Disconnecting...\n");
	ioctl(nbd_fd, NBD_DISCONNECT);
	pthread_join(doit_tid, NULL);
	ioctl(nbd_fd, NBD_CLEAR_SOCK);
	close(sv[0]);
	close(sv[1]);
	pthread_join(server_tid, NULL);

	printf("\nDone. Check dmesg:\n");
	printf("  sudo dmesg | grep -iE 'double reply|unexpected reply|wrong magic|dead conn'\n");

	close(nbd_fd);
	return 0;

cleanup_nbd:
    close(nbd_fd);
cleanup_socks:
    close(sv[0]);
    close(sv[1]);
    return ret;
}
