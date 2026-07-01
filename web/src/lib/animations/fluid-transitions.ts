import type { Variants } from 'motion/react';

// 缓动函数
export const EASING = {
    easeOutCubic: [0.25, 0.46, 0.45, 0.94] as const,
    easeOutExpo: [0.16, 1, 0.3, 1] as const,
    easeInOutCubic: [0.65, 0, 0.35, 1] as const,
    easeOutQuart: [0.25, 1, 0.5, 1] as const,
} as const;

// Spring 配置
export const SPRING = {
    smooth: {
        type: "spring" as const,
        stiffness: 80,
        damping: 20,
        mass: 1.2,
    },
    gentle: {
        type: "spring" as const,
        stiffness: 70,
        damping: 18,
        mass: 1.5,
    },
    bouncy: {
        type: "spring" as const,
        stiffness: 100,
        damping: 15,
        mass: 1,
    },
} as const;

/**
 * 磁性吸附进入动画
 */
export const ENTRANCE_VARIANTS = {
    // 导航栏进入
    navbar: {
        initial: {
            opacity: 0,
            scale: 0.2,
            filter: "blur(10px)",
        },
        animate: {
            opacity: 1,
            scale: 1,
            filter: "blur(0px)",
            transition: SPRING.smooth,
        },
    } as Variants,

    // 主内容进入
    content: {
        initial: {
            scale: 0.8,
            opacity: 0,
        },
        animate: {
            scale: 1,
            opacity: 1,
            transition: {
                duration: 0.5,
                ease: EASING.easeOutExpo,
                delay: 0.1,
            },
        },
    } as Variants,

    // 头部进入
    header: {
        initial: {
            y: 100,
            opacity: 0,
            filter: "blur(10px)",
        },
        animate: {
            y: 0,
            opacity: 1,
            filter: "blur(0px)",
            transition: {
                duration: 0.5,
                ease: EASING.easeOutExpo,
                delay: 0.1,
            },
        },
    } as Variants,

};

