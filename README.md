# Boot Animation Studio - Companion Module

This Magisk/KernelSU module acts as a seamless bridge between your Android device and the Web Studio, allowing you to inject, manage, and test custom boot animations directly.

## Features

- Automatically injects boot animations created from the website.
- It automatically detects the boot animation path of your phone.
- Allows you to pull the original boot animation and edit it.
- It automatically pulls the native screen resolution of your phone.
- Creates a history of up to 5 applied animations, allowing you to reapply them whenever you want. 
- You can remove the custom animation at any time with a single click on the website.
- And much more!
  

## Requirements
1. **Root Access**

2. **Your system must support standard `.zip` boot animations.**
   > **Note:** Stock Samsung devices use a proprietary `.qmg` format and are **NOT** supported by this module.

## Installation & Usage
1. Download the latest `BootCreator-vX.X.zip` from the **[Releases](../../releases)** tab.
2. Flash the module.
3. **Reboot** your device.
4. Open the [Boot Animation Studio Website](https://lumii55.github.io/boot_animation_studio/) on your phone or PC
6. Click **"Connect to Phone"**, allow the prompt on your device's screen, and start creating!

## Uninstallation
To remove the module and restore your original boot animation, simply delete the module from Magisk/KernelSU and reboot. The module cleans up all custom paths automatically.
