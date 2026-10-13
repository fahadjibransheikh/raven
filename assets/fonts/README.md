# Wine Tahoma

`wine-tahoma.woff2` and `wine-tahoma-bold.woff2` are the Wine project's Tahoma
replacement, used by the Express theme. They are not Microsoft's Tahoma.

- Copyright (c) 2004 Larry Snyder, based on Bitstream Vera Sans, copyright (c)
  2003 Bitstream, Inc.
- License: GNU Lesser General Public License, version 2.1 or later. See
  `COPYING.LIB` in this directory.
- Source: `fonts/tahoma.sfd` and `fonts/tahomabd.sfd` in the Wine repository
  (https://gitlab.winehq.org/wine/wine), release 11.0.

The files were converted from Wine 11.0's built TTFs to WOFF2 with fontTools.
The embedded bitmap strikes (EBDT/EBLC tables) were removed so text renders
from the outlines; the glyphs and metrics are unchanged.
