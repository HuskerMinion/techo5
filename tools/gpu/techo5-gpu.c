// SPDX-License-Identifier: MIT
//
// techo5-gpu: the GPU for echod on the mainline 2nd gen Show 8 (Mali-G72) and 2nd gen Show 5
// (Mali-T720), through Mesa's panfrost. One process owns the GPU and the panel, and echod asks it, over
// a SOCK_SEQPACKET socket on fd 3, for two things:
//
//   - the screen: echod draws its landscape canvas into shared memory as it always has; this uploads
//     it, draws it rotated onto the portrait panel, and shows it with a page flip at vblank (DRM/KMS
//     through GBM, so no tearing and no rotation on the cores). A video frame is drawn the same way,
//     with the canvas's controls blended over it.
//   - the camera: a packed 10-bit Bayer frame is unpacked, demosaiced (bilinear, edges mirrored) and
//     put through echod's tone tables in a fragment shader, and read back as RGBA, or as YUYV for the
//     Show 8's JPEG encoder.
//
// The shaders are OpenGL ES 2.0, the most the Show 5's Mali-T720 offers. Should this process be
// missing or die, echod draws and develops on the cores as it did before.
//
// Messages are struct req one way and struct rep the other; buffers are memfds passed once, with
// SCM_RIGHTS, and mapped on both sides.

#define _GNU_SOURCE
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <GLES2/gl2.h>
#include <GLES2/gl2ext.h>
#include <errno.h>
#include <fcntl.h>
#include <gbm.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>
#include <xf86drm.h>
#include <xf86drmMode.h>
#if defined(__aarch64__)
#include <arm_neon.h>
#endif

enum {
	OP_HELLO = 1,
	OP_DISPLAY_OPEN = 2,	// a[0..2] canvas w, h, rotated; fd: the canvas (w*h*4)
	OP_PRESENT = 3,		// draw the canvas; a[0] bit 0: measure it first, the reply's a[0] then being its
				// brightest red, green or blue value plus 1
	OP_PRESENT_FRAME = 4,	// a[0..1] frame w, h in panel orientation, a[2..5] overlay rect in canvas
				// coordinates (x0, y0, x1, y1), a[6] bit 0: the frame's bytes are B, G, R, A (the
				// framebuffer's order), else R, G, B, A; fd: the frame (w*h*4), passed when it changes
	OP_DEVELOP = 5,		// a[0..4] w, h, bpl, blue first, out; fds: raw, tables (3*2048 bytes), out,
				// passed when they change. out: 0 RGBA (w*h*4) and 1 YUYV (w*h*2) read back into
				// the out buffer; 3 YUV 4:2:0 planes (Y, Cb, Cr: w*h*3/2) read back likewise; 2
				// YUYV left in a linear buffer of the GPU's whose dmabuf the reply carries the
				// first time (a[0] 1 when it does, a[1] its stride), for a JPEG encoder to read in
				// place: on these SoCs the CPU reads GPU memory uncached, about 160 MB/s, so a
				// picture read back costs more than developing it.
	OP_BLANK = 6,		// a[0]: 1 off, 0 on
	OP_COLOR = 8,		// the display pipeline's own color blocks (CCORR and GAMMA, as the CRTC's CTM and
				// GAMMA_LUT): a[0] bit 0 a matrix is given, else the identity; bit 1 curves are given,
				// else none (the identity, should a curve have been set before); fd: struct color,
				// passed when it changes. The reply's a[0] is GAMMA_LUT_SIZE, a[1] bit 0 there is a
				// CTM, bit 1 a GAMMA_LUT. a[0] 0 with no CTM or LUT ever set costs nothing.
	OP_LIFT = 9,		// the gain every present from now draws its pixels with, for a backlight turned down
				// by as much (echod's screen/lift.go): a[0] the gain in 1/65536, 0 or 65536 and
				// under being none; a[1] bit 0: draw the last canvas again with it now, which is
				// -EAGAIN when the last present was not the canvas alone.
};

// struct color is OP_COLOR's buffer: a 3x3 matrix applied to the panel's R, G, B (row-major, out =
// m * in, 16.16 fixed point), then one curve a channel, 256 points over the input's range, 0..65535.
struct color {
	int32_t m[9];
	uint16_t curve[3][256];
};

struct req {
	uint32_t op;
	uint32_t a[7];
};

struct rep {
	int32_t status;		// 0, or a negative errno
	uint32_t a[3];
	char text[112];
};

static const char *log_name = "techo5-gpu";
static int debug; // TECHO5_GPU_DEBUG set: time each development's steps on stderr

static double now_ms(void)
{
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return t.tv_sec * 1e3 + t.tv_nsec / 1e6;
}
#define say(...) do { fprintf(stderr, "%s: ", log_name); fprintf(stderr, __VA_ARGS__); fputc('\n', stderr); } while (0)

// ---- shared buffers ----

// MAX_SIDE caps every picture's width and height (and a raw frame's bytes a line at twice it), so the
// sizes worked out from a request, done in 64 bits, stay far from overflowing anything.
#define MAX_SIDE 8192

struct shm {
	int fd;
	void *p;
	size_t size;
};

// shm_take puts the memfd *fd, when one was passed, in m's place, and takes it: *fd becomes -1, and
// it is closed here if it cannot be used. Either way m must then hold need bytes. Nothing of m changes
// unless the new buffer is mapped and big enough: a request refused leaves what was there.
static int shm_take(struct shm *m, int *fd, uint64_t need)
{
	if (*fd < 0)
		return m->p && m->size >= need ? 0 : -EINVAL;
	int f = *fd;
	*fd = -1;
	off_t size = lseek(f, 0, SEEK_END);
	if (size <= 0 || (uint64_t)size < need || (uint64_t)size > SIZE_MAX) {
		close(f);
		return -EINVAL;
	}
	void *p = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, f, 0);
	if (p == MAP_FAILED) {
		int err = -errno;
		close(f);
		return err;
	}
	if (m->p)
		munmap(m->p, m->size);
	if (m->fd >= 0)
		close(m->fd);
	m->fd = f, m->p = p, m->size = size;
	return 0;
}

// side_ok is whether a width or height is one a request may ask for.
static int side_ok(uint32_t v) { return v > 0 && v <= MAX_SIDE; }

// ---- GL ----

static GLuint shader(GLenum type, const char *src)
{
	GLuint s = glCreateShader(type);
	glShaderSource(s, 1, &src, NULL);
	glCompileShader(s);
	GLint ok;
	glGetShaderiv(s, GL_COMPILE_STATUS, &ok);
	if (!ok) {
		char log[1024];
		glGetShaderInfoLog(s, sizeof(log), NULL, log);
		say("shader: %s", log);
		return 0;
	}
	return s;
}

static GLuint program(const char *vs, const char *fs)
{
	GLuint v = shader(GL_VERTEX_SHADER, vs), f = shader(GL_FRAGMENT_SHADER, fs);
	if (!v || !f)
		return 0;
	GLuint p = glCreateProgram();
	glAttachShader(p, v);
	glAttachShader(p, f);
	glBindAttribLocation(p, 0, "pos");
	glLinkProgram(p);
	GLint ok;
	glGetProgramiv(p, GL_LINK_STATUS, &ok);
	if (!ok) {
		char log[1024];
		glGetProgramInfoLog(p, sizeof(log), NULL, log);
		say("link: %s", log);
		return 0;
	}
	return p;
}

// One full-target quad. The fragment shaders work out their pixel from uv, the target's coordinates
// from 0 to 1, bottom-up, interpolated at full precision. Not from gl_FragCoord: OpenGL ES 2.0 gives it
// only medium precision, a 16-bit float on these GPUs, which cannot hold x + 0.5 past 1024, so on the
// Show 8 every odd column beyond 1024 came out as its even neighbor.
static const char *vs_quad =
	"attribute vec2 pos;\n"
	"varying highp vec2 uv;\n"
	"void main() { uv = pos * 0.5 + 0.5; gl_Position = vec4(pos, 0.0, 1.0); }\n";

static void quad(void)
{
	static const GLfloat v[] = { -1, -1, 1, -1, -1, 1, 1, 1 };
	glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, v);
	glEnableVertexAttribArray(0);
	glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
}

// Rows. GL counts the window's rows from the bottom (gl_FragCoord.y), while the panel scans out from the
// top and textures hold their first uploaded row, the top one, at t = 0. So the panel row from the top
// is panelh - gl_FragCoord.y, and texture coordinates are taken in top-down picture coordinates. (The
// camera passes draw into a texture and read it back in the same row order, so they need no turn.)
//
// The lift: with the backlight turned down to 1/gain^2.2 of what was asked, every pixel is drawn gain
// times as bright, so the panel shows what it would have. Drawn to an 8-bit buffer, a plain gain would
// skip a code every 1/(gain-1) of them, a step that can show in a smooth dark gradient; an ordered dither
// (4x4 Bayer, from the panel pixel p) puts each pixel on one of its two neighboring codes instead, on
// average at the exact value. The code is taken back to its integer first: a sampler's 8-bit value is
// not exactly k/255 on every GPU (llvmpipe's is a little under), and the dither's margin is 1/32. A gain of 1 leaves the pixel exactly as it was. Nothing clips that the
// gain was chosen for (echod keeps the canvas's brightest value times the gain at 255 or under); what is
// over is held at 255.
#define LIFT_GLSL \
	"uniform float gain;\n" \
	"float bayer2(vec2 a) { return 2.0 * a.x + 3.0 * a.y - 4.0 * a.x * a.y; }\n" \
	"vec3 lift(vec3 c, vec2 p) {\n" \
	"  if (gain <= 1.0) return c;\n" \
	"  vec2 a = mod(floor(p), 4.0);\n" \
	"  float m = 4.0 * bayer2(mod(a, 2.0)) + bayer2(floor(a / 2.0));\n" \
	"  vec3 k = floor(c * 255.0 + 0.5);\n" \
	"  return min(floor(k * gain + (m + 0.5) / 16.0), 255.0) / 255.0;\n" \
	"}\n"

// The canvas onto the panel: panel pixel (px, py) shows canvas (py, panelW-1-px) when rotated, as
// hardware/screen's rotate does. The canvas is premultiplied RGBA; the panel has no alpha.
static const char *fs_canvas =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D canvas;\n"
	"uniform vec2 csize;\n"		// canvas w, h
	"uniform float panelw;\n"
	"uniform float panelh;\n"
	"uniform float rotated;\n"
	"uniform vec4 clip;\n"		// canvas x0, y0, x1, y1 to draw, or the whole canvas
	"uniform float blend;\n"	// 1: keep the canvas's alpha, to be blended over a frame
	LIFT_GLSL
	"void main() {\n"
	"  vec2 p = vec2(uv.x * panelw, (1.0 - uv.y) * panelh);\n"
	"  vec2 c = rotated > 0.5 ? vec2(p.y, panelw - p.x) : p;\n"
	"  if (c.x < clip.x || c.y < clip.y || c.x >= clip.z || c.y >= clip.w) discard;\n"
	"  vec4 t = texture2D(canvas, c / csize);\n"
	"  gl_FragColor = blend > 0.5 ? vec4(t.rgb * max(gain, 1.0), t.a) : vec4(lift(t.rgb, p), 1.0);\n"
	"}\n";

// A video frame, already in the panel's orientation, top row first like the canvas.
static const char *fs_frame =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D frame;\n"
	"uniform float panelw;\n"
	"uniform float panelh;\n"
	"uniform float bgra;\n"	// 1: the frame's bytes are B, G, R, A
	LIFT_GLSL
	"void main() {\n"
	"  vec2 p = vec2(uv.x * panelw, (1.0 - uv.y) * panelh);\n"
	"  vec3 c = texture2D(frame, vec2(uv.x, 1.0 - uv.y)).rgb;\n"
	"  gl_FragColor = vec4(lift(bgra > 0.5 ? c.bgr : c, p), 1.0);\n"
	"}\n";

// The camera, in two passes. Unpack: the raw frame is a LUMINANCE texture of its bytes, bpl wide, four
// 10-bit samples in five bytes, least significant bits first, so sample k of a group is made of bytes k
// and k+1 of it: floor(lo / 4^k) + mod(hi, 4^(k+1)) * 256 / 4^k, with no branch on k. Each sample is
// unpacked once, into an RGBA texture of the frame's size (high eight bits in r, low two in g), rather
// than by every neighbor that reads it.
static const char *fs_unpack =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D raw;\n"
	"uniform vec2 size;\n"		// w, h in pixels
	"uniform float bpl;\n"
	"void main() {\n"
	"  float x = floor(uv.x * size.x), y = (floor(uv.y * size.y) + 0.5) / size.y;\n"
	"  float grp = floor(x / 4.0), k = x - grp * 4.0, b = grp * 5.0 + k;\n"
	"  float lo = floor(texture2D(raw, vec2((b + 0.5) / bpl, y)).r * 255.0 + 0.5);\n"
	"  float hi = floor(texture2D(raw, vec2((b + 1.5) / bpl, y)).r * 255.0 + 0.5);\n"
	"  float p = exp2(2.0 * k);\n"
	"  float v = floor(lo / p) + mod(hi, 4.0 * p) * (256.0 / p);\n"
	"  float h8 = floor(v / 4.0);\n"
	"  gl_FragColor = vec4(h8 / 255.0, (v - h8 * 4.0) / 255.0, 0.0, 1.0);\n"
	"}\n";

// Develop: bilinear demosaic of the unpacked samples (edges mirrored, as Full does), then the tone
// tables, a 2048x3 LUMINANCE texture indexed by twice a sample (they run on the 11-bit scale of a
// summed green pair). Nine reads a pixel, and the colors picked by the pixel's place in its Bayer
// cell with arithmetic, not branches: neighboring pixels sit at all four places, and a GPU running
// them together would otherwise pay for every branch on each.
static const char *fs_develop =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D samples;\n"
	"uniform sampler2D tables;\n"
	"uniform vec2 size;\n"
	"uniform float bluefirst;\n"
	"float at(float x, float y) {\n"
	"  x = x < 0.0 ? 1.0 : (x >= size.x ? size.x - 2.0 : x);\n"
	"  y = y < 0.0 ? 1.0 : (y >= size.y ? size.y - 2.0 : y);\n"
	"  vec2 t = texture2D(samples, (vec2(x, y) + 0.5) / size).rg;\n"
	"  return floor(t.r * 255.0 + 0.5) * 4.0 + floor(t.g * 255.0 + 0.5);\n"
	"}\n"
	"float lut(float row, float v) { return texture2D(tables, vec2((2.0 * v + 0.5) / 2048.0, (row + 0.5) / 3.0)).r; }\n"
	"void main() {\n"
	"  float x = floor(uv.x * size.x), y = floor(uv.y * size.y);\n"
	"  float c = at(x, y);\n"
	"  float l = at(x - 1.0, y), r = at(x + 1.0, y), u = at(x, y - 1.0), d = at(x, y + 1.0);\n"
	"  float ul = at(x - 1.0, y - 1.0), ur = at(x + 1.0, y - 1.0), dl = at(x - 1.0, y + 1.0), dr = at(x + 1.0, y + 1.0);\n"
	"  float hor = floor((l + r) / 2.0), ver = floor((u + d) / 2.0);\n"
	"  float cross = floor((l + r + u + d) / 4.0), diag = floor((ul + ur + dl + dr) / 4.0);\n"
	"  float ox = mod(x, 2.0), oy = mod(y, 2.0);\n"
	"  float s0 = (1.0 - ox) * (1.0 - oy), s1 = ox * (1.0 - oy), s2 = (1.0 - ox) * oy, s3 = ox * oy;\n"
	// red site: (c, cross, diag); green on a red row: (hor, c, ver); green on a blue row: (ver, c,
	// hor); blue site: (diag, cross, c), before any swap of red and blue.
	"  vec3 rgb = s0 * vec3(c, cross, diag) + s1 * vec3(hor, c, ver) + s2 * vec3(ver, c, hor) + s3 * vec3(diag, cross, c);\n"
	"  if (bluefirst > 0.5) rgb = rgb.bgr;\n"
	"  gl_FragColor = vec4(lut(0.0, rgb.r), lut(1.0, rgb.g), lut(2.0, rgb.b), 1.0);\n"
	"}\n";

// RGBA to YUYV: one output texel per two pixels, (Y0, Cb, Y1, Cr), BT.601 full range as JFIF has it,
// the pair's chroma their average; the bytes read back are the encoder's YUYV as they are.
static const char *fs_yuyv =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D rgb;\n"
	"uniform vec2 size;\n"		// the RGBA picture's w, h
	"void main() {\n"
	"  float x = floor(uv.x * size.x * 0.5) * 2.0, y = floor(uv.y * size.y) + 0.5;\n"
	"  vec3 a = texture2D(rgb, vec2((x + 0.5) / size.x, y / size.y)).rgb;\n"
	"  vec3 b = texture2D(rgb, vec2((x + 1.5) / size.x, y / size.y)).rgb;\n"
	"  float y0 = dot(a, vec3(0.299, 0.587, 0.114)), y1 = dot(b, vec3(0.299, 0.587, 0.114));\n"
	"  vec3 m = (a + b) * 0.5;\n"
	"  float cb = dot(m, vec3(-0.168736, -0.331264, 0.5)) + 128.0 / 255.0;\n"
	"  float cr = dot(m, vec3(0.5, -0.418688, -0.081312)) + 128.0 / 255.0;\n"
	"  gl_FragColor = vec4(y0, cb, y1, cr);\n"
	"}\n";

// YUV 4:2:0, one plane a pass: each output texel is four bytes of the plane, so a plane of pw x ph
// bytes is drawn pw/4 x ph and read back as it is. plane 0 is Y; 1 and 2 are Cb and Cr, each the
// average of a 2x2 block of pixels. BT.601 full range, as JFIF has it.
static const char *fs_planar =
	"precision highp float;\n"
	"varying highp vec2 uv;\n"
	"uniform sampler2D rgb;\n"
	"uniform vec2 size;\n"		// the RGBA picture's w, h
	"uniform float plane;\n"
	"vec3 px(float x, float y) { return texture2D(rgb, (vec2(x, y) + 0.5) / size).rgb; }\n"
	"float value(vec3 c) {\n"
	"  if (plane < 0.5) return dot(c, vec3(0.299, 0.587, 0.114));\n"
	"  if (plane < 1.5) return dot(c, vec3(-0.168736, -0.331264, 0.5)) + 128.0 / 255.0;\n"
	"  return dot(c, vec3(0.5, -0.418688, -0.081312)) + 128.0 / 255.0;\n"
	"}\n"
	"void main() {\n"
	"  vec4 o;\n"
	"  if (plane < 0.5) {\n"
	"    float x = floor(uv.x * size.x / 4.0) * 4.0, y = floor(uv.y * size.y);\n"
	"    o = vec4(value(px(x, y)), value(px(x + 1.0, y)), value(px(x + 2.0, y)), value(px(x + 3.0, y)));\n"
	"  } else {\n"
	"    float x = floor(uv.x * size.x / 8.0) * 8.0, y = floor(uv.y * size.y / 2.0) * 2.0;\n"
	"    vec4 v;\n"
	"    for (int i = 0; i < 4; i++) {\n"
	"      float cx = x + float(i) * 2.0;\n"
	"      v[i] = value((px(cx, y) + px(cx + 1.0, y) + px(cx, y + 1.0) + px(cx + 1.0, y + 1.0)) * 0.25);\n"
	"    }\n"
	"    o = v;\n"
	"  }\n"
	"  gl_FragColor = o;\n"
	"}\n";

// Extension entry points, looked up at start: the libraries do not export them to link against.
static PFNEGLCREATEIMAGEKHRPROC create_image;
static PFNEGLDESTROYIMAGEKHRPROC destroy_image;
static PFNGLEGLIMAGETARGETTEXTURE2DOESPROC image_texture;

static struct {
	EGLDisplay dpy;
	EGLContext ctx;
	EGLConfig cfg;
	struct gbm_device *gbm;
	int drm;
	char gpu[64];

	// screen
	int open;
	drmModeModeInfo mode;
	uint32_t conn, crtc;
	struct gbm_surface *surf;
	EGLSurface esurf;
	struct gbm_bo *shown;
	uint32_t shown_fb;
	int crtc_set;
	struct shm canvas;
	GLuint canvas_tex;
	uint32_t cw, ch, rotated;
	GLuint p_canvas, p_frame;
	struct shm frame;
	GLuint frame_tex;
	uint32_t fw, fh;
	// the CRTC's color properties (0 when it has none), and whether either was ever set
	uint32_t ctm_prop, lut_prop, lut_size;
	int ctm_set, lut_set;
	struct shm color;
	// the lift's gain (OP_LIFT), and whether the last present was the canvas alone, which OP_LIFT
	// can draw again from its texture
	float gain;
	int canvas_shown;

	// camera
	GLuint p_unpack, p_develop, p_yuyv, p_planar;
	struct shm raw, tables, out;
	GLuint raw_tex, tab_tex, smp_tex, rgb_tex, yuyv_tex, smp_fbo, rgb_fbo, yuyv_fbo;
	GLuint plane_tex[3], plane_fbo[3];
	uint32_t plw, plh; // the size the plane targets were made for
	// the dmabuf YUYV target, for its size
	struct gbm_bo *dma_bo;
	EGLImageKHR dma_img;
	GLuint dma_tex, dma_fbo;
	uint32_t dma_w, dma_h;
	uint32_t dw, dh, dbpl;
} g = { .drm = -1, .canvas.fd = -1, .frame.fd = -1, .raw.fd = -1, .tables.fd = -1, .out.fd = -1,
	.color.fd = -1, .gain = 1 };

static GLuint texture(void)
{
	GLuint t;
	glGenTextures(1, &t);
	glBindTexture(GL_TEXTURE_2D, t);
	glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_NEAREST);
	glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_NEAREST);
	glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE);
	glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
	return t;
}

// The panel's DRM device: the card with a connected connector. GBM on it is Mesa's kmsro, which
// renders on panfrost and allocates buffers the display can scan out.
static int open_card(void)
{
	char path[32];
	for (int i = 0; i < 8; i++) {
		snprintf(path, sizeof(path), "/dev/dri/card%d", i);
		int fd = open(path, O_RDWR | O_CLOEXEC);
		if (fd < 0)
			continue;
		drmModeRes *res = drmModeGetResources(fd);
		int found = 0;
		for (int c = 0; res && c < res->count_connectors && !found; c++) {
			drmModeConnector *conn = drmModeGetConnector(fd, res->connectors[c]);
			if (conn && conn->connection == DRM_MODE_CONNECTED && conn->count_modes > 0)
				found = 1;
			drmModeFreeConnector(conn);
		}
		drmModeFreeResources(res);
		if (found)
			return fd;
		close(fd);
	}
	return -ENODEV;
}

static int gl_start(void)
{
	g.drm = open_card();
	if (g.drm < 0) {
		say("no DRM card with a panel");
		return g.drm;
	}
	g.gbm = gbm_create_device(g.drm);
	if (!g.gbm)
		return -ENODEV;
	PFNEGLGETPLATFORMDISPLAYEXTPROC get = (void *)eglGetProcAddress("eglGetPlatformDisplayEXT");
	g.dpy = get ? get(EGL_PLATFORM_GBM_KHR, g.gbm, NULL) : eglGetDisplay((EGLNativeDisplayType)g.gbm);
	if (g.dpy == EGL_NO_DISPLAY || !eglInitialize(g.dpy, NULL, NULL))
		return -ENODEV;
	eglBindAPI(EGL_OPENGL_ES_API);
	EGLint attrs[] = { EGL_SURFACE_TYPE, EGL_WINDOW_BIT, EGL_RED_SIZE, 8, EGL_GREEN_SIZE, 8,
		EGL_BLUE_SIZE, 8, EGL_ALPHA_SIZE, 0, EGL_RENDERABLE_TYPE, EGL_OPENGL_ES2_BIT, EGL_NONE };
	EGLConfig cfgs[64];
	EGLint n = 0;
	eglChooseConfig(g.dpy, attrs, cfgs, 64, &n);
	// The config whose native visual is the scanout format.
	for (int i = 0; i < n; i++) {
		EGLint id;
		eglGetConfigAttrib(g.dpy, cfgs[i], EGL_NATIVE_VISUAL_ID, &id);
		if (id == GBM_FORMAT_XRGB8888) {
			g.cfg = cfgs[i];
			break;
		}
	}
	if (!g.cfg)
		return -ENODEV;
	EGLint cattrs[] = { EGL_CONTEXT_CLIENT_VERSION, 2, EGL_NONE };
	g.ctx = eglCreateContext(g.dpy, g.cfg, EGL_NO_CONTEXT, cattrs);
	if (g.ctx == EGL_NO_CONTEXT || !eglMakeCurrent(g.dpy, EGL_NO_SURFACE, EGL_NO_SURFACE, g.ctx))
		return -ENODEV;
	create_image = (void *)eglGetProcAddress("eglCreateImageKHR");
	destroy_image = (void *)eglGetProcAddress("eglDestroyImageKHR");
	image_texture = (void *)eglGetProcAddress("glEGLImageTargetTexture2DOES");
	glPixelStorei(GL_UNPACK_ALIGNMENT, 1);
	glPixelStorei(GL_PACK_ALIGNMENT, 1);
	g.p_canvas = program(vs_quad, fs_canvas);
	g.p_frame = program(vs_quad, fs_frame);
	g.p_unpack = program(vs_quad, fs_unpack);
	g.p_develop = program(vs_quad, fs_develop);
	g.p_yuyv = program(vs_quad, fs_yuyv);
	g.p_planar = program(vs_quad, fs_planar);
	if (!g.p_canvas || !g.p_frame || !g.p_unpack || !g.p_develop || !g.p_yuyv || !g.p_planar)
		return -EINVAL;
	snprintf(g.gpu, sizeof(g.gpu), "%s", (const char *)glGetString(GL_RENDERER));
	return 0;
}

// ---- the screen ----

static void flip_done(int fd, unsigned int seq, unsigned int s, unsigned int us, void *data)
{
	*(int *)data = 0;
}

static uint32_t bo_fb(struct gbm_bo *bo)
{
	uint32_t *fb = gbm_bo_get_user_data(bo);
	if (fb)
		return *fb;
	fb = malloc(sizeof(*fb));
	if (!fb)
		return 0;
	uint32_t handles[4] = { gbm_bo_get_handle(bo).u32 }, pitches[4] = { gbm_bo_get_stride(bo) }, offsets[4] = { 0 };
	if (drmModeAddFB2(g.drm, gbm_bo_get_width(bo), gbm_bo_get_height(bo), GBM_FORMAT_XRGB8888,
			  handles, pitches, offsets, fb, 0)) {
		free(fb);
		return 0;
	}
	gbm_bo_set_user_data(bo, fb, NULL);
	return *fb;
}

static void color_props(void);
static int set_color(uint32_t flags, const struct color *c);

static int display_open(struct req *r, int *fd, struct rep *out)
{
	uint32_t w = r->a[0], h = r->a[1];
	if (!side_ok(w) || !side_ok(h))
		return -EINVAL;
	int err = shm_take(&g.canvas, fd, (uint64_t)w * h * 4);
	if (err)
		return err;
	if (!g.open) {
		drmModeRes *res = drmModeGetResources(g.drm);
		if (!res)
			return -ENODEV;
		drmModeConnector *conn = NULL;
		for (int c = 0; c < res->count_connectors; c++) {
			conn = drmModeGetConnector(g.drm, res->connectors[c]);
			if (conn && conn->connection == DRM_MODE_CONNECTED && conn->count_modes)
				break;
			drmModeFreeConnector(conn);
			conn = NULL;
		}
		if (!conn) {
			drmModeFreeResources(res);
			return -ENODEV;
		}
		drmModeEncoder *enc = conn->encoder_id ? drmModeGetEncoder(g.drm, conn->encoder_id) : NULL;
		uint32_t crtc = enc && enc->crtc_id ? enc->crtc_id : res->count_crtcs > 0 ? res->crtcs[0] : 0;
		drmModeFreeEncoder(enc);
		if (!crtc) {
			drmModeFreeConnector(conn);
			drmModeFreeResources(res);
			return -ENODEV;
		}
		g.mode = conn->modes[0];
		for (int m = 0; m < conn->count_modes; m++)
			if (conn->modes[m].type & DRM_MODE_TYPE_PREFERRED)
				g.mode = conn->modes[m];
		g.conn = conn->connector_id;
		g.crtc = crtc;
		drmModeFreeConnector(conn);
		drmModeFreeResources(res);
		g.surf = gbm_surface_create(g.gbm, g.mode.hdisplay, g.mode.vdisplay, GBM_FORMAT_XRGB8888,
					    GBM_BO_USE_SCANOUT | GBM_BO_USE_RENDERING);
		if (!g.surf)
			return -ENOMEM;
		g.esurf = eglCreateWindowSurface(g.dpy, g.cfg, (EGLNativeWindowType)g.surf, NULL);
		if (g.esurf == EGL_NO_SURFACE) {
			gbm_surface_destroy(g.surf);
			g.surf = NULL;
			return -ENOMEM;
		}
		g.canvas_tex = texture();
		g.open = 1;
		color_props();
		if (g.ctm_set || g.lut_set)
			set_color(0, NULL);
	}
	g.cw = w, g.ch = h, g.rotated = r->a[2] & 1;
	if (!eglMakeCurrent(g.dpy, g.esurf, g.esurf, g.ctx))
		return -EIO;
	glBindTexture(GL_TEXTURE_2D, g.canvas_tex);
	glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, g.cw, g.ch, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
	g.canvas_shown = 0;
	out->a[0] = g.mode.hdisplay, out->a[1] = g.mode.vdisplay, out->a[2] = g.mode.vrefresh;
	return 0;
}

// vblank_running is whether the CRTC's vblank counter moves: three frames' time and more at 60 Hz.
// A driver without the sequence ioctl is taken at its word.
static int vblank_running(void)
{
	uint64_t a, b, ns;
	if (drmCrtcGetSequence(g.drm, g.crtc, &a, &ns))
		return 1;
	usleep(60000);
	if (drmCrtcGetSequence(g.drm, g.crtc, &b, &ns))
		return 1;
	return b != a;
}

// show swaps the drawn buffer onto the panel: the first time with a mode set, after that with a page
// flip, waiting for it so the caller is paced by the panel and the buffer it frees is truly free.
static int show(void)
{
	if (!eglSwapBuffers(g.dpy, g.esurf))
		return -EIO;
	struct gbm_bo *bo = gbm_surface_lock_front_buffer(g.surf);
	if (!bo)
		return -EIO;
	uint32_t fb = bo_fb(bo);
	if (!fb) {
		gbm_surface_release_buffer(g.surf, bo);
		return -EIO;
	}
	int err;
	if (!g.crtc_set) {
		err = drmModeSetCrtc(g.drm, g.crtc, fb, 0, 0, &g.conn, 1, &g.mode) ? -errno : 0;
		// Said, for the log: a pipeline that never starts (the Show 5's DSI could stall at the first
		// enable after the bootloader's handover, before patches/0016) shows here first. Turning the
		// CRTC off and on does not bring it back, so nothing more is tried.
		if (!err && !vblank_running())
			say("no vblank after the first modeset: the display pipeline has not started");
		g.crtc_set = !err;
	} else {
		static int pending; // static: a flip's event can come after this frame has given up on it
		drmEventContext ev = { .version = 2, .page_flip_handler = flip_done };
		pending = 1;
		err = drmModePageFlip(g.drm, g.crtc, fb, DRM_MODE_PAGE_FLIP_EVENT, &pending) ? -errno : 0;
		while (!err && pending) {
			struct pollfd p = { .fd = g.drm, .events = POLLIN };
			if (poll(&p, 1, 200) <= 0)
				break;
			drmHandleEvent(g.drm, &ev);
		}
	}
	if (g.shown)
		gbm_surface_release_buffer(g.surf, g.shown);
	g.shown = bo;
	return err;
}

static void draw_canvas(float blend, float x0, float y0, float x1, float y1)
{
	glUseProgram(g.p_canvas);
	glActiveTexture(GL_TEXTURE0);
	glBindTexture(GL_TEXTURE_2D, g.canvas_tex);
	glUniform1i(glGetUniformLocation(g.p_canvas, "canvas"), 0);
	glUniform2f(glGetUniformLocation(g.p_canvas, "csize"), g.cw, g.ch);
	glUniform1f(glGetUniformLocation(g.p_canvas, "panelw"), g.mode.hdisplay);
	glUniform1f(glGetUniformLocation(g.p_canvas, "panelh"), g.mode.vdisplay);
	glUniform1f(glGetUniformLocation(g.p_canvas, "rotated"), g.rotated);
	glUniform4f(glGetUniformLocation(g.p_canvas, "clip"), x0, y0, x1, y1);
	glUniform1f(glGetUniformLocation(g.p_canvas, "blend"), blend);
	glUniform1f(glGetUniformLocation(g.p_canvas, "gain"), g.gain);
	quad();
}

// brightest is the highest red, green or blue value of n RGBA pixels: what the lift may raise to 255.
// 0.6 ms for the Show 8's 1280x800 canvas and about 0.9 ms for the Show 5's 960x480, read at about
// the memory's speed; only while the lift is on.
static unsigned brightest(const uint8_t *p, size_t n)
{
	size_t i = 0, bytes = n * 4;
	unsigned top = 0;
#if defined(__aarch64__)
	static const uint8_t rgb[16] = { 255, 255, 255, 0, 255, 255, 255, 0, 255, 255, 255, 0, 255, 255, 255, 0 };
	uint8x16_t m0 = vdupq_n_u8(0), m1 = m0, m2 = m0, m3 = m0;
	for (; i + 64 <= bytes; i += 64) {
		m0 = vmaxq_u8(m0, vld1q_u8(p + i));
		m1 = vmaxq_u8(m1, vld1q_u8(p + i + 16));
		m2 = vmaxq_u8(m2, vld1q_u8(p + i + 32));
		m3 = vmaxq_u8(m3, vld1q_u8(p + i + 48));
	}
	m0 = vmaxq_u8(vmaxq_u8(m0, m1), vmaxq_u8(m2, m3));
	top = vmaxvq_u8(vandq_u8(m0, vld1q_u8(rgb)));
#endif
	for (; i < bytes; i++)
		if (i % 4 != 3 && p[i] > top)
			top = p[i];
	return top;
}

// redraw draws the canvas last presented again, from its texture, with the gain as it is now.
static int redraw(void)
{
	if (!g.open || !g.canvas_shown)
		return -EAGAIN;
	if (!eglMakeCurrent(g.dpy, g.esurf, g.esurf, g.ctx))
		return -EIO;
	glBindFramebuffer(GL_FRAMEBUFFER, 0);
	glViewport(0, 0, g.mode.hdisplay, g.mode.vdisplay);
	glDisable(GL_BLEND);
	draw_canvas(0, 0, 0, g.cw, g.ch);
	return show();
}

static int lift(struct req *r)
{
	uint32_t q = r->a[0];
	if (q > 4 * 65536)
		return -EINVAL;
	g.gain = q > 65536 ? q / 65536.0f : 1.0f;
	return (r->a[1] & 1) ? redraw() : 0;
}

static int present(struct req *r, struct rep *out)
{
	if (!g.open)
		return -ENODEV;
	if (!eglMakeCurrent(g.dpy, g.esurf, g.esurf, g.ctx))
		return -EIO;
	if (r->a[0] & 1)
		out->a[0] = brightest(g.canvas.p, (size_t)g.cw * g.ch) + 1;
	g.canvas_shown = 1;
	glBindTexture(GL_TEXTURE_2D, g.canvas_tex);
	glTexSubImage2D(GL_TEXTURE_2D, 0, 0, 0, g.cw, g.ch, GL_RGBA, GL_UNSIGNED_BYTE, g.canvas.p);
	glBindFramebuffer(GL_FRAMEBUFFER, 0);
	glViewport(0, 0, g.mode.hdisplay, g.mode.vdisplay);
	glDisable(GL_BLEND);
	draw_canvas(0, 0, 0, g.cw, g.ch);
	return show();
}

static int present_frame(struct req *r, int *fd)
{
	if (!g.open)
		return -ENODEV;
	uint32_t w = r->a[0], h = r->a[1];
	if (!side_ok(w) || !side_ok(h))
		return -EINVAL;
	int err = shm_take(&g.frame, fd, (uint64_t)w * h * 4);
	if (err)
		return err;
	if (!eglMakeCurrent(g.dpy, g.esurf, g.esurf, g.ctx))
		return -EIO;
	if (!g.frame_tex)
		g.frame_tex = texture();
	glBindTexture(GL_TEXTURE_2D, g.frame_tex);
	if (w != g.fw || h != g.fh) {
		glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, w, h, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
		g.fw = w, g.fh = h;
	}
	glTexSubImage2D(GL_TEXTURE_2D, 0, 0, 0, w, h, GL_RGBA, GL_UNSIGNED_BYTE, g.frame.p);
	glBindFramebuffer(GL_FRAMEBUFFER, 0);
	glViewport(0, 0, g.mode.hdisplay, g.mode.vdisplay);
	glDisable(GL_BLEND);
	glUseProgram(g.p_frame);
	glUniform1i(glGetUniformLocation(g.p_frame, "frame"), 0);
	glUniform2f(glGetUniformLocation(g.p_frame, "fsize"), w, h);
	glUniform1f(glGetUniformLocation(g.p_frame, "panelw"), g.mode.hdisplay);
	glUniform1f(glGetUniformLocation(g.p_frame, "panelh"), g.mode.vdisplay);
	glUniform1f(glGetUniformLocation(g.p_frame, "gain"), g.gain);
	glUniform1f(glGetUniformLocation(g.p_frame, "bgra"), r->a[6] & 1);
	g.canvas_shown = 0;
	quad();
	if (r->a[4] > r->a[2] && r->a[5] > r->a[3]) {
		// The controls: the canvas's rectangle, premultiplied, over the frame.
		glBindTexture(GL_TEXTURE_2D, g.canvas_tex);
		glTexSubImage2D(GL_TEXTURE_2D, 0, 0, 0, g.cw, g.ch, GL_RGBA, GL_UNSIGNED_BYTE, g.canvas.p);
		glEnable(GL_BLEND);
		glBlendFunc(GL_ONE, GL_ONE_MINUS_SRC_ALPHA);
		draw_canvas(1, r->a[2], r->a[3], r->a[4], r->a[5]);
		glDisable(GL_BLEND);
	}
	return show();
}


static int blank(int off)
{
	if (!g.open)
		return -ENODEV;
	drmModeConnector *conn = drmModeGetConnector(g.drm, g.conn);
	if (!conn)
		return -ENODEV;
	int err = -ENOENT;
	for (int i = 0; i < conn->count_props; i++) {
		drmModePropertyRes *p = drmModeGetProperty(g.drm, conn->props[i]);
		if (p && !strcmp(p->name, "DPMS"))
			err = drmModeConnectorSetProperty(g.drm, g.conn, p->prop_id,
							  off ? DRM_MODE_DPMS_OFF : DRM_MODE_DPMS_ON) ? -errno : 0;
		drmModeFreeProperty(p);
	}
	drmModeFreeConnector(conn);
	return err;
}

// ---- color ----

// color_props finds the CRTC's CTM and GAMMA_LUT, and puts back to the identity either one that a
// process before this one left set: a tint must not outlive the echod that asked for it.
static void color_props(void)
{
	drmModeObjectProperties *pr = drmModeObjectGetProperties(g.drm, g.crtc, DRM_MODE_OBJECT_CRTC);
	uint64_t ctm = 0, lut = 0;
	for (uint32_t i = 0; pr && i < pr->count_props; i++) {
		drmModePropertyRes *p = drmModeGetProperty(g.drm, pr->props[i]);
		if (!p)
			continue;
		if (!strcmp(p->name, "CTM"))
			g.ctm_prop = p->prop_id, ctm = pr->prop_values[i];
		else if (!strcmp(p->name, "GAMMA_LUT"))
			g.lut_prop = p->prop_id, lut = pr->prop_values[i];
		else if (!strcmp(p->name, "GAMMA_LUT_SIZE"))
			g.lut_size = pr->prop_values[i];
		drmModeFreeProperty(p);
	}
	drmModeFreeObjectProperties(pr);
	// An MT8163 device tree from before its CCORR's fallback was mediatek,mt8183-disp-ccorr has the
	// driver write 1.0 as 0x800, which the block reads as -2.0: no matrix there rather than a wrong one.
	char compat[128] = { 0 };
	int fd = open("/proc/device-tree/soc/ccorr@1400d000/compatible", O_RDONLY | O_CLOEXEC);
	if (fd >= 0) {
		ssize_t n = read(fd, compat, sizeof(compat) - 1);
		close(fd);
		for (ssize_t i = 0; i < n; i++)
			if (!compat[i])
				compat[i] = ' ';
		if (strstr(compat, "mt8163-disp-ccorr") && strstr(compat, "mt8192-disp-ccorr")) {
			say("the CCORR's device tree gives the wrong coefficient scale; no color matrix");
			g.ctm_prop = 0, ctm = 0;
		}
	}
	if (!g.lut_prop || g.lut_size < 2)
		g.lut_prop = 0, g.lut_size = 0;
	g.ctm_set = ctm != 0, g.lut_set = lut != 0;
}

// set_blob sets prop to a blob of data, a commit that waits for the vblank it takes effect at.
static int set_blob(uint32_t prop, const void *data, size_t size)
{
	uint32_t blob;
	if (drmModeCreatePropertyBlob(g.drm, data, size, &blob))
		return -errno;
	int err = drmModeObjectSetProperty(g.drm, g.crtc, DRM_MODE_OBJECT_CRTC, prop, blob) ? -errno : 0;
	drmModeDestroyPropertyBlob(g.drm, blob); // the CRTC's state keeps its own reference
	return err;
}

// set_color puts c (or the identity, for what is not given) into the CRTC's color blocks. Mainline
// mediatek-drm leaves a block as it was when its property is cleared, so the identity is set, not
// nothing: the MT8183's CCORR at the identity passes pixels through exactly (1.0 is 1024 there), and
// its GAMMA stays in relay until a curve is first set.
static int set_color(uint32_t flags, const struct color *c)
{
	int err = 0;
	if (g.ctm_prop && ((flags & 1) || g.ctm_set)) {
		struct drm_color_ctm ctm;
		for (int i = 0; i < 9; i++) {
			int64_t v = (flags & 1) ? c->m[i] : (i % 4 == 0 ? 65536 : 0);
			uint64_t mag = (uint64_t)(v < 0 ? -v : v) << 16; // 16.16 to S31.32, sign and magnitude
			ctm.matrix[i] = v < 0 ? mag | 1ULL << 63 : mag;
		}
		err = set_blob(g.ctm_prop, &ctm, sizeof(ctm));
		g.ctm_set = 1;
	}
	if (!err && g.lut_prop && ((flags & 2) || g.lut_set)) {
		struct drm_color_lut *lut = calloc(g.lut_size, sizeof(*lut));
		if (!lut)
			return -ENOMEM;
		for (uint32_t k = 0; k < g.lut_size; k++) {
			uint32_t x = k * 255 * 256 / (g.lut_size - 1); // the input, 8.8 fixed point
			uint32_t i = x >> 8, f = x & 255, j = i < 255 ? i + 1 : 255;
			uint16_t *ch[3] = { &lut[k].red, &lut[k].green, &lut[k].blue };
			for (int n = 0; n < 3; n++)
				*ch[n] = (flags & 2) ? (uint16_t)((c->curve[n][i] * (256 - f) + c->curve[n][j] * f) >> 8)
						     : (uint16_t)(x * 65535 / (255 * 256));
		}
		err = set_blob(g.lut_prop, lut, g.lut_size * sizeof(*lut));
		free(lut);
		g.lut_set = 1;
	}
	return err;
}

static int color(struct req *r, int *fd, struct rep *out)
{
	if (!g.open)
		return -ENODEV;
	uint32_t flags = r->a[0] & 3;
	int err = 0;
	if (flags && (err = shm_take(&g.color, fd, sizeof(struct color))))
		return err;
	err = set_color(flags, g.color.p);
	out->a[0] = g.lut_size;
	out->a[1] = (g.ctm_prop ? 1 : 0) | (g.lut_prop ? 2 : 0);
	return err;
}

// ---- the camera ----

static int target(GLuint *tex, GLuint *fbo, uint32_t w, uint32_t h)
{
	if (!*tex) {
		*tex = texture();
		glGenFramebuffers(1, fbo);
	}
	glBindTexture(GL_TEXTURE_2D, *tex);
	glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, w, h, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
	glBindFramebuffer(GL_FRAMEBUFFER, *fbo);
	glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, *tex, 0);
	return glCheckFramebufferStatus(GL_FRAMEBUFFER) == GL_FRAMEBUFFER_COMPLETE ? 0 : -EINVAL;
}

// dma_target makes the linear buffer a YUYV picture of w x h is drawn into, w/2 x h RGBA texels
// whose bytes in memory are R, G, B, A (DRM's ABGR8888), and exports it as a dmabuf.
static int dma_target(uint32_t w, uint32_t h, int *fd, uint32_t *stride)
{
	*fd = -1;
	if (g.dma_bo && g.dma_w == w && g.dma_h == h)
		return 0;
	if (g.dma_bo) {
		glDeleteFramebuffers(1, &g.dma_fbo);
		glDeleteTextures(1, &g.dma_tex);
		destroy_image(g.dpy, g.dma_img);
		gbm_bo_destroy(g.dma_bo);
		g.dma_bo = NULL;
	}
	struct gbm_bo *bo = gbm_bo_create(g.gbm, w / 2, h, GBM_FORMAT_ABGR8888, GBM_BO_USE_RENDERING | GBM_BO_USE_LINEAR);
	if (!bo)
		return -ENOMEM;
	int dfd = gbm_bo_get_fd(bo);
	EGLint attrs[] = {
		EGL_WIDTH, (EGLint)(w / 2), EGL_HEIGHT, (EGLint)h,
		EGL_LINUX_DRM_FOURCC_EXT, GBM_FORMAT_ABGR8888,
		EGL_DMA_BUF_PLANE0_FD_EXT, dfd,
		EGL_DMA_BUF_PLANE0_OFFSET_EXT, 0,
		EGL_DMA_BUF_PLANE0_PITCH_EXT, (EGLint)gbm_bo_get_stride(bo),
		EGL_NONE };
	if (!create_image || !image_texture) {
		close(dfd);
		gbm_bo_destroy(bo);
		return -ENOSYS;
	}
	EGLImageKHR img = create_image(g.dpy, EGL_NO_CONTEXT, EGL_LINUX_DMA_BUF_EXT, NULL, attrs);
	if (img == EGL_NO_IMAGE_KHR) {
		close(dfd);
		gbm_bo_destroy(bo);
		return -EINVAL;
	}
	glGenTextures(1, &g.dma_tex);
	glBindTexture(GL_TEXTURE_2D, g.dma_tex);
	image_texture(GL_TEXTURE_2D, img);
	glGenFramebuffers(1, &g.dma_fbo);
	glBindFramebuffer(GL_FRAMEBUFFER, g.dma_fbo);
	glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, g.dma_tex, 0);
	if (glCheckFramebufferStatus(GL_FRAMEBUFFER) != GL_FRAMEBUFFER_COMPLETE) {
		glBindFramebuffer(GL_FRAMEBUFFER, 0);
		glDeleteFramebuffers(1, &g.dma_fbo);
		glDeleteTextures(1, &g.dma_tex);
		g.dma_fbo = g.dma_tex = 0;
		destroy_image(g.dpy, img);
		gbm_bo_destroy(bo);
		close(dfd);
		return -EINVAL;
	}
	g.dma_bo = bo, g.dma_img = img, g.dma_w = w, g.dma_h = h;
	*fd = dfd;
	*stride = gbm_bo_get_stride(bo);
	return 0;
}

static int develop(struct req *r, int fds[3], struct rep *out, int *out_fd)
{
	uint32_t w = r->a[0], h = r->a[1], bpl = r->a[2], bluefirst = r->a[3], mode = r->a[4];
	int err;
	if (mode > 3 || !side_ok(w) || !side_ok(h) || bpl == 0 || bpl > 2 * MAX_SIDE ||
	    (uint64_t)bpl * 4 < (uint64_t)w * 5 || w % 8 || h % 2)
		return -EINVAL;
	uint64_t need = mode == 0 ? (uint64_t)w * h * 4 : mode == 3 ? (uint64_t)w * h * 3 / 2 : (uint64_t)w * h * 2;
	if ((err = shm_take(&g.raw, &fds[0], (uint64_t)bpl * h)) || (err = shm_take(&g.tables, &fds[1], 3 * 2048)))
		return err;
	// The out buffer is taken whenever it is passed, used or not, so a later read-back finds it mapped.
	if ((fds[2] >= 0 || mode != 2) && (err = shm_take(&g.out, &fds[2], mode == 2 ? 1 : need)))
		return err;
	if (!eglMakeCurrent(g.dpy, EGL_NO_SURFACE, EGL_NO_SURFACE, g.ctx))
		return -EIO;
	if (!g.raw_tex) {
		g.raw_tex = texture();
		g.tab_tex = texture();
	}
	glActiveTexture(GL_TEXTURE0);
	glBindTexture(GL_TEXTURE_2D, g.raw_tex);
	if (w != g.dw || h != g.dh || bpl != g.dbpl) {
		glTexImage2D(GL_TEXTURE_2D, 0, GL_LUMINANCE, bpl, h, 0, GL_LUMINANCE, GL_UNSIGNED_BYTE, NULL);
		if ((err = target(&g.smp_tex, &g.smp_fbo, w, h)) || (err = target(&g.rgb_tex, &g.rgb_fbo, w, h)) ||
		    (err = target(&g.yuyv_tex, &g.yuyv_fbo, w / 2, h)))
			return err;
		g.dw = w, g.dh = h, g.dbpl = bpl;
		glActiveTexture(GL_TEXTURE0);
		glBindTexture(GL_TEXTURE_2D, g.raw_tex);
	}
	double t0 = debug ? now_ms() : 0;
	glTexSubImage2D(GL_TEXTURE_2D, 0, 0, 0, bpl, h, GL_LUMINANCE, GL_UNSIGNED_BYTE, g.raw.p);
	glActiveTexture(GL_TEXTURE1);
	glBindTexture(GL_TEXTURE_2D, g.tab_tex);
	glTexImage2D(GL_TEXTURE_2D, 0, GL_LUMINANCE, 2048, 3, 0, GL_LUMINANCE, GL_UNSIGNED_BYTE, g.tables.p);

	glDisable(GL_BLEND);
	glBindFramebuffer(GL_FRAMEBUFFER, g.smp_fbo);
	glViewport(0, 0, w, h);
	glUseProgram(g.p_unpack);
	glActiveTexture(GL_TEXTURE0);
	glBindTexture(GL_TEXTURE_2D, g.raw_tex);
	glUniform1i(glGetUniformLocation(g.p_unpack, "raw"), 0);
	glUniform2f(glGetUniformLocation(g.p_unpack, "size"), w, h);
	glUniform1f(glGetUniformLocation(g.p_unpack, "bpl"), bpl);
	quad();

	glBindFramebuffer(GL_FRAMEBUFFER, g.rgb_fbo);
	glUseProgram(g.p_develop);
	glActiveTexture(GL_TEXTURE0);
	glBindTexture(GL_TEXTURE_2D, g.smp_tex);
	glUniform1i(glGetUniformLocation(g.p_develop, "samples"), 0);
	glUniform1i(glGetUniformLocation(g.p_develop, "tables"), 1);
	glUniform2f(glGetUniformLocation(g.p_develop, "size"), w, h);
	glUniform1f(glGetUniformLocation(g.p_develop, "bluefirst"), bluefirst);
	quad();
	double t1 = 0, t2 = 0;
	if (debug) {
		t1 = now_ms();
		glFinish();
		t2 = now_ms();
	}
	if (mode == 1 || mode == 2) {
		if (mode == 2) {
			uint32_t stride = 0;
			if ((err = dma_target(w, h, out_fd, &stride)))
				return err;
			if (*out_fd >= 0)
				out->a[0] = 1, out->a[1] = stride;
			glBindFramebuffer(GL_FRAMEBUFFER, g.dma_fbo);
		} else {
			glBindFramebuffer(GL_FRAMEBUFFER, g.yuyv_fbo);
		}
		glViewport(0, 0, w / 2, h);
		glUseProgram(g.p_yuyv);
		glActiveTexture(GL_TEXTURE0);
		glBindTexture(GL_TEXTURE_2D, g.rgb_tex);
		glUniform1i(glGetUniformLocation(g.p_yuyv, "rgb"), 0);
		glUniform2f(glGetUniformLocation(g.p_yuyv, "size"), w, h);
		quad();
		if (mode == 1)
			glReadPixels(0, 0, w / 2, h, GL_RGBA, GL_UNSIGNED_BYTE, g.out.p);
		else
			glFinish(); // the encoder reads it as soon as the reply is out
	} else if (mode == 3) {
		uint8_t *dst = g.out.p;
		for (int pl = 0; pl < 3; pl++) {
			uint32_t pw = pl ? w / 2 : w, ph = pl ? h / 2 : h;
			if (!g.plane_tex[pl] || w != g.plw || h != g.plh) {
				if ((err = target(&g.plane_tex[pl], &g.plane_fbo[pl], pw / 4, ph)))
					return err;
				if (pl == 2)
					g.plw = w, g.plh = h;
			}
			glBindFramebuffer(GL_FRAMEBUFFER, g.plane_fbo[pl]);
			glViewport(0, 0, pw / 4, ph);
			glUseProgram(g.p_planar);
			glActiveTexture(GL_TEXTURE0);
			glBindTexture(GL_TEXTURE_2D, g.rgb_tex);
			glUniform1i(glGetUniformLocation(g.p_planar, "rgb"), 0);
			glUniform2f(glGetUniformLocation(g.p_planar, "size"), w, h);
			glUniform1f(glGetUniformLocation(g.p_planar, "plane"), pl);
			quad();
			glReadPixels(0, 0, pw / 4, ph, GL_RGBA, GL_UNSIGNED_BYTE, dst);
			dst += (size_t)pw * ph;
		}
	} else {
		glReadPixels(0, 0, w, h, GL_RGBA, GL_UNSIGNED_BYTE, g.out.p);
	}
	if (debug)
		say("develop %ux%u mode %u: upload+submit %.1f ms, shader %.1f ms, convert+out %.1f ms",
		    w, h, mode, t1 - t0, t2 - t1, now_ms() - t2);
	glBindFramebuffer(GL_FRAMEBUFFER, 0);
	return glGetError() == GL_NO_ERROR ? 0 : -EIO;
}

// ---- the socket ----

static int receive(int sock, struct req *r, int fds[3])
{
	char cbuf[CMSG_SPACE(3 * sizeof(int))];
	struct iovec iov = { r, sizeof(*r) };
	struct msghdr m = { .msg_iov = &iov, .msg_iovlen = 1, .msg_control = cbuf, .msg_controllen = sizeof(cbuf) };
	fds[0] = fds[1] = fds[2] = -1;
	ssize_t n = recvmsg(sock, &m, MSG_CMSG_CLOEXEC);
	if (n <= 0)
		return n == 0 ? 0 : -1;
	// Up to three descriptors are kept; any more, or any with a message of the wrong size, are closed.
	int kept = 0;
	for (struct cmsghdr *c = CMSG_FIRSTHDR(&m); c; c = CMSG_NXTHDR(&m, c))
		if (c->cmsg_level == SOL_SOCKET && c->cmsg_type == SCM_RIGHTS) {
			int k = (c->cmsg_len - CMSG_LEN(0)) / sizeof(int);
			int got[8];
			memcpy(got, CMSG_DATA(c), (k > 8 ? 8 : k) * sizeof(int));
			for (int i = 0; i < k && i < 8; i++) {
				if (n == sizeof(*r) && kept < 3)
					fds[kept++] = got[i];
				else
					close(got[i]);
			}
		}
	return n == sizeof(*r) ? 1 : -1;
}

// reset_color is `techo5-gpu --reset-color`: the CRTC's color blocks put back to the identity, and
// nothing else (no GL). The kernel keeps a CTM and GAMMA_LUT set on the CRTC after the process that set
// them is gone, so a helper that died with the night's tint on left it on the panel, under the
// framebuffer echod draws on instead, which tints on the cores as well. echod runs this once the
// helper has exited.
static int reset_color(void)
{
	g.drm = open_card();
	if (g.drm < 0)
		return 1;
	drmModeRes *res = drmModeGetResources(g.drm);
	if (!res)
		return 1;
	for (int c = 0; c < res->count_connectors && !g.crtc; c++) {
		drmModeConnector *conn = drmModeGetConnector(g.drm, res->connectors[c]);
		if (conn && conn->connection == DRM_MODE_CONNECTED && conn->count_modes) {
			drmModeEncoder *enc = conn->encoder_id ? drmModeGetEncoder(g.drm, conn->encoder_id) : NULL;
			g.crtc = enc && enc->crtc_id ? enc->crtc_id : res->count_crtcs > 0 ? res->crtcs[0] : 0;
			drmModeFreeEncoder(enc);
		}
		drmModeFreeConnector(conn);
	}
	drmModeFreeResources(res);
	if (!g.crtc)
		return 1;
	color_props();
	if (!g.ctm_set && !g.lut_set)
		return 0;
	int err = set_color(0, NULL);
	if (err)
		say("could not put the panel's color back: %s", strerror(-err));
	return err ? 1 : 0;
}

int main(int argc, char **argv)
{
	if (argc > 1 && !strcmp(argv[1], "--reset-color"))
		return reset_color();
	int sock = 3;
	debug = getenv("TECHO5_GPU_DEBUG") != NULL;
	int err = gl_start();
	struct req r;
	int fds[3];
	for (;;) {
		int got = receive(sock, &r, fds);
		if (got <= 0)
			break;
		struct rep out = { 0 };
		int out_fd = -1;
		if (err)
			out.status = err;
		else
			switch (r.op) {
			case OP_HELLO:
				snprintf(out.text, sizeof(out.text), "%s", g.gpu);
				break;
			case OP_DISPLAY_OPEN:
				out.status = display_open(&r, &fds[0], &out);
				break;
			case OP_PRESENT:
				out.status = present(&r, &out);
				break;
			case OP_PRESENT_FRAME:
				out.status = present_frame(&r, &fds[0]);
				break;
			case OP_DEVELOP:
				out.status = develop(&r, fds, &out, &out_fd);
				break;
			case OP_BLANK:
				out.status = blank(r.a[0]);
				break;
			case OP_COLOR:
				out.status = color(&r, &fds[0], &out);
				break;
			case OP_LIFT:
				out.status = lift(&r);
				break;
			default:
				out.status = -ENOSYS;
			}
		// Whatever was passed and not taken (shm_take sets what it takes to -1) is closed: a request
		// that uses no buffer, or was refused, must not leave descriptors behind.
		for (int i = 0; i < 3; i++)
			if (fds[i] >= 0)
				close(fds[i]);
		char cbuf[CMSG_SPACE(sizeof(int))];
		struct iovec iov = { &out, sizeof(out) };
		struct msghdr m = { .msg_iov = &iov, .msg_iovlen = 1 };
		if (out_fd >= 0) {
			m.msg_control = cbuf, m.msg_controllen = sizeof(cbuf);
			struct cmsghdr *c = CMSG_FIRSTHDR(&m);
			c->cmsg_level = SOL_SOCKET, c->cmsg_type = SCM_RIGHTS, c->cmsg_len = CMSG_LEN(sizeof(int));
			memcpy(CMSG_DATA(c), &out_fd, sizeof(int));
		}
		ssize_t sent = sendmsg(sock, &m, 0);
		if (out_fd >= 0)
			close(out_fd);
		if (sent != sizeof(out))
			break;
	}
	return 0;
}
