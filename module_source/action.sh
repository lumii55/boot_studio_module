#!/system/bin/sh
MODDIR="${0%/*}"
LOGFILE="$MODDIR/boot_creator.log"

echo "==================================="
echo "       BOOT CREATOR LOGS           "
echo "==================================="
echo ""

if [ -f "$LOGFILE" ]; then
    tail -n 50 "$LOGFILE"
else
    echo "No logs found yet!"
fi

echo ""
echo "==================================="
echo "          END OF LOGS              "
echo "==================================="
