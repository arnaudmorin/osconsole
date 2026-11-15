# Fonts

`vga8x16.psf` is the 8x16 VGA font of SeaVGABIOS, the VGA BIOS shipped with
QEMU, which is what QEMU draws on screen in VGA text mode. It is used to
recognize characters on VNC consoles.

The bitmaps are the `vgafont16` table of SeaBIOS
[`vgasrc/vgafonts.c`](https://gitlab.com/qemu-project/seabios/-/blob/master/vgasrc/vgafonts.c),
wrapped in a PSF1 header (256 glyphs, code page 437 order, no Unicode
table). As stated in that file:

> These fonts come from ftp://ftp.simtel.net/pub/simtelnet/msdos/screen/fntcol16.zip
> The package is (c) by Joseph Gil
> The individual fonts are public domain
