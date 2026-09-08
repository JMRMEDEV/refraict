#!/usr/bin/env bash
# Throwaway stress harness for the synthetic-template bold detector.
# For each real OCR token (word + bbox) on a UI page, it renders the SAME word
# in several font families at Regular / Semibold / Bold, measures swPerH for each
# with collagemeasure, AVERAGES per weight class (font-agnostic reference), then
# classifies the real token by nearest averaged reference — but only ACCEPTS the
# call when the nearest reference is clearly closer than the runner-up (strict
# margin); otherwise reports "uncertain".
set -u

CM=${CM:-/tmp/cm}
PS=140          # synthetic pointsize (large, for stroke granularity)
UP=6            # upscale factor for the real crop
MARGIN=${MARGIN:-0.35}   # accept only if (2nd-1st)/(2nd) >= MARGIN

# Reference font files per weight class (averaged).
REG=(/usr/share/fonts/truetype/open-sans/OpenSans-Regular.ttf
     /usr/share/fonts/truetype/noto/NotoSans-Regular.ttf
     /usr/share/fonts/opentype/cantarell/Cantarell-Regular.otf
     /usr/share/fonts/truetype/dejavu/DejaVuSans.ttf)
SEMI=(/usr/share/fonts/truetype/open-sans/OpenSans-Semibold.ttf)  # only genuine semibold installed
BOLD=(/usr/share/fonts/truetype/open-sans/OpenSans-Bold.ttf
      /usr/share/fonts/truetype/noto/NotoSans-Bold.ttf
      /usr/share/fonts/opentype/cantarell/Cantarell-Bold.otf
      /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf)

# swPerH of a rendered word for a specific font file.
render_sw() { # word fontfile
  local w="$1" f="$2" out=/tmp/_syn.png
  convert -size 700x220 xc:white -gravity center -pointsize $PS -font "$f" -annotate 0 "$w" "$out" 2>/dev/null || return 1
  "$CM" -crop 0,0,700,220 "$out" | awk 'NR==2{print $4}'
}

avg_sw() { # word "fontfile fontfile ..."
  local w="$1"; shift
  local sum=0 n=0 v
  for f in "$@"; do
    [ -f "$f" ] || continue
    v=$(render_sw "$w" "$f") || continue
    sum=$(awk -v s="$sum" -v x="$v" 'BEGIN{print s+x}')
    n=$((n+1))
  done
  [ $n -gt 0 ] && awk -v s="$sum" -v n="$n" 'BEGIN{printf "%.4f", s/n}' || echo 0
}

IMG="$1"; shift
echo "== $IMG (margin=$MARGIN) =="
printf "%-14s %6s | %6s %6s %6s | %-9s %s\n" token realSW reg semi bold class note
for spec in "$@"; do
  W="${spec%%:*}"; BB="${spec#*:}"
  RS=$("$CM" -crop "$BB" -up $UP "$IMG" | awk 'NR==2{print $4}')
  R=$(avg_sw "$W" "${REG[@]}")
  S=$(avg_sw "$W" "${SEMI[@]}")
  B=$(avg_sw "$W" "${BOLD[@]}")
  read CLASS NOTE < <(awk -v rs="$RS" -v r="$R" -v s="$S" -v b="$B" -v m="$MARGIN" '
    function ad(x,y){return (x>y)?x-y:y-x}
    BEGIN{
      dr=ad(rs,r); ds=ad(rs,s); db=ad(rs,b);
      # find nearest and runner-up
      split("regular semibold bold", nm, " ");
      d[1]=dr; d[2]=ds; d[3]=db;
      i1=1; for(i=2;i<=3;i++) if(d[i]<d[i1]) i1=i;
      i2=0; for(i=1;i<=3;i++){ if(i==i1) continue; if(i2==0||d[i]<d[i2]) i2=i }
      margin=(d[i2]>0)?(d[i2]-d[i1])/d[i2]:1;
      if(margin>=m) print nm[i1], sprintf("(m=%.2f)", margin);
      else print "uncertain", sprintf("(near %s/%s m=%.2f)", nm[i1], nm[i2], margin);
    }')
  printf "%-14s %6s | %6s %6s %6s | %-9s %s\n" "$W" "$RS" "$R" "$S" "$B" "$CLASS" "$NOTE"
done
