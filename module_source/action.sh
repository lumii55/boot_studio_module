#!/system/bin/sh
MODDIR="${0%/*}"
LOGFILE="$MODDIR/boot_creator.log"
PREVIOUS_LOGFILE="$MODDIR/boot_creator.previous.log"

echo "==================================="
echo "       BOOT CREATOR LOGS           "
echo "==================================="
echo ""

echo "CURRENT BOOT"
echo "-----------------------------------"
if [ -f "$LOGFILE" ]; then
    tail -n 50 "$LOGFILE"
else
    echo "No logs found for the current boot."
fi

echo ""
echo "PREVIOUS BOOT"
echo "-----------------------------------"
if [ -f "$PREVIOUS_LOGFILE" ]; then
    tail -n 50 "$PREVIOUS_LOGFILE"
else
    echo "No logs found for the previous boot."
fi

echo ""
echo "==================================="
echo "          END OF LOGS              "
echo "==================================="
