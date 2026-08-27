//! 无锁 SPSC (Single-Producer Single-Consumer) 环形队列
//!
//! 用于媒体帧在解码→编码管道中的零等待传递。
//! 基于 atomic sequence number, 单生产者单消费者场景下无需锁。
//!
//! 适用场景:
//!   - 解码线程 → 编码线程的 YuvFrame 传递
//!   - 网络接收线程 → 处理线程的 RTP 包传递
//!   - 任何单生产者单消费者的帧/包传递场景
//!
//! 不适用场景 (用 crossbeam-channel 或 tokio::sync::mpsc 代替):
//!   - 多生产者 (MPSC)
//!   - 多消费者 (MCSC)
//!   - 需要异步 await 的场景

use std::cell::UnsafeCell;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::Arc;

/// 无锁 SPSC 环形队列
///
/// 固定容量，预分配槽位。生产者写入 write_pos，消费者读取 read_pos。
/// 使用 SeqCst ordering 确保跨线程可见性。
///
/// 容量必须是 2 的幂以便用位运算取模。
pub struct SpscQueue<T> {
    buffer: Box<[UnsafeCell<Option<T>>]>,
    capacity: usize,
    mask: usize,
    write_pos: AtomicU64,
    read_pos: AtomicU64,
    /// 统计: 总写入数
    total_written: AtomicU64,
    /// 统计: 总读取数
    total_read: AtomicU64,
    /// 统计: 因满而丢弃的次数
    dropped_full: AtomicUsize,
}

unsafe impl<T: Send> Send for SpscQueue<T> {}
unsafe impl<T: Send> Sync for SpscQueue<T> {}

impl<T> SpscQueue<T> {
    /// 创建 SPSC 队列，容量会向上取整到 2 的幂。
    pub fn new(capacity: usize) -> Self {
        let cap = capacity.next_power_of_two();
        let mut buffer = Vec::with_capacity(cap);
        for _ in 0..cap {
            buffer.push(UnsafeCell::new(None));
        }
        Self {
            buffer: buffer.into_boxed_slice(),
            capacity: cap,
            mask: cap - 1,
            write_pos: AtomicU64::new(0),
            read_pos: AtomicU64::new(0),
            total_written: AtomicU64::new(0),
            total_read: AtomicU64::new(0),
            dropped_full: AtomicUsize::new(0),
        }
    }

    /// 尝试入队。如果队列满则丢弃并返回 false。
    #[inline]
    pub fn try_push(&self, item: T) -> bool {
        let wpos = self.write_pos.load(Ordering::Relaxed);
        let rpos = self.read_pos.load(Ordering::Acquire);
        let used = (wpos - rpos) as usize;
        if used >= self.capacity {
            self.dropped_full.fetch_add(1, Ordering::Relaxed);
            return false;
        }
        let idx = (wpos as usize) & self.mask;
        unsafe {
            *self.buffer[idx].get() = Some(item);
        }
        self.write_pos.store(wpos + 1, Ordering::Release);
        self.total_written.fetch_add(1, Ordering::Relaxed);
        true
    }

    /// 尝试出队。如果队列空则返回 None。
    #[inline]
    pub fn try_pop(&self) -> Option<T> {
        let rpos = self.read_pos.load(Ordering::Relaxed);
        let wpos = self.write_pos.load(Ordering::Acquire);
        if rpos >= wpos {
            return None;
        }
        let idx = (rpos as usize) & self.mask;
        let item = unsafe { (*self.buffer[idx].get()).take() };
        self.read_pos.store(rpos + 1, Ordering::Release);
        self.total_read.fetch_add(1, Ordering::Relaxed);
        item
    }

    /// 当前队列中的元素数量（近似值，并发场景下可能不准确）
    #[inline]
    pub fn len(&self) -> usize {
        let wpos = self.write_pos.load(Ordering::Relaxed);
        let rpos = self.read_pos.load(Ordering::Relaxed);
        (wpos - rpos) as usize
    }

    /// 队列是否为空
    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// 队列容量
    #[inline]
    pub fn capacity(&self) -> usize {
        self.capacity
    }

    /// 总写入数
    pub fn total_written(&self) -> u64 {
        self.total_written.load(Ordering::Relaxed)
    }

    /// 总读取数
    pub fn total_read(&self) -> u64 {
        self.total_read.load(Ordering::Relaxed)
    }

    /// 因队列满而丢弃的次数
    pub fn dropped_full(&self) -> usize {
        self.dropped_full.load(Ordering::Relaxed)
    }
}

impl<T> Drop for SpscQueue<T> {
    fn drop(&mut self) {
        // 清理剩余元素
        for slot in self.buffer.iter_mut() {
            unsafe {
                *slot.get_mut() = None;
            }
        }
    }
}

/// 共享的 SPSC 队列（Arc 包装）
pub type SharedSpscQueue<T> = Arc<SpscQueue<T>>;

/// 创建共享 SPSC 队列
pub fn shared_spsc_queue<T>(capacity: usize) -> SharedSpscQueue<T> {
    Arc::new(SpscQueue::new(capacity))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread;

    #[test]
    fn test_basic_push_pop() {
        let q = SpscQueue::new(16);
        assert!(q.is_empty());
        assert!(q.try_push(1));
        assert!(q.try_push(2));
        assert_eq!(q.len(), 2);
        assert_eq!(q.try_pop(), Some(1));
        assert_eq!(q.try_pop(), Some(2));
        assert!(q.is_empty());
        assert_eq!(q.try_pop(), None);
    }

    #[test]
    fn test_full_queue_drops() {
        let q = SpscQueue::new(4);
        // capacity 向上取整到 4
        assert!(q.try_push(1));
        assert!(q.try_push(2));
        assert!(q.try_push(3));
        assert!(q.try_push(4));
        assert!(!q.try_push(5)); // 满，丢弃
        assert_eq!(q.dropped_full(), 1);
    }

    #[test]
    fn test_wrap_around() {
        let q = SpscQueue::new(4);
        for _ in 0..100 {
            // push 2, pop 2, 验证环绕
            assert!(q.try_push(1));
            assert!(q.try_push(2));
            assert_eq!(q.try_pop(), Some(1));
            assert_eq!(q.try_pop(), Some(2));
        }
        assert_eq!(q.total_written(), 200);
        assert_eq!(q.total_read(), 200);
    }

    #[test]
    fn test_concurrent_spsc() {
        let q = Arc::new(SpscQueue::new(1024));
        let q2 = q.clone();
        let q3 = q.clone();
        let n = 100_000;

        let producer = thread::spawn(move || {
            for i in 0..n {
                while !q2.try_push(i) {
                    std::hint::spin_loop();
                }
            }
        });

        let consumer = thread::spawn(move || {
            let mut count = 0;
            let mut last = -1i64;
            while count < n {
                if let Some(v) = q3.try_pop() {
                    assert_eq!(
                        v as i64,
                        last + 1,
                        "out of order: got {} expected {}",
                        v,
                        last + 1
                    );
                    last = v as i64;
                    count += 1;
                } else {
                    std::hint::spin_loop();
                }
            }
            count
        });

        producer.join().unwrap();
        let received = consumer.join().unwrap();
        assert_eq!(received, n);
        assert_eq!(q.total_written(), n as u64);
        assert_eq!(q.total_read(), n as u64);
    }

    #[test]
    fn test_capacity_rounding() {
        let q = SpscQueue::<u8>::new(5);
        // 5 向上取整到 8
        assert_eq!(q.capacity(), 8);
    }
}
