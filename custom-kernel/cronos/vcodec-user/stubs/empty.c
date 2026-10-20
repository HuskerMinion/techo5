/*
 * Soname-only placeholder. It is built as libutils.so (NEEDED by the kept
 * vendor libmtk_drvb.so, which imports nothing from it) and as the software
 * codec libraries NEEDED by libvcodecdrv.so (libmp4enc_sa.ca7.so,
 * libvc1dec_sa.ca7.so, libvp8enc_sa.ca7.so, libhevce_sb.ca7.android.so).
 * libvcodecdrv imports no symbols from them and only dlopen()s them for
 * MPEG4/VC1/VP8/HEVC software codecs, which the harness does not use.
 */
int vcodec_user_placeholder;
